package build

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	memcache "github.com/doodlescheduling/flux-build/internal/cache"
	"github.com/doodlescheduling/flux-build/internal/helm/chart"
	chartcache "github.com/doodlescheduling/flux-build/internal/helm/chart/cache"
	"github.com/doodlescheduling/flux-build/internal/helm/getter"
	"github.com/doodlescheduling/flux-build/internal/helm/postrenderer"
	"github.com/doodlescheduling/flux-build/internal/helm/registry"
	"github.com/doodlescheduling/flux-build/internal/helm/repository"
	soci "github.com/doodlescheduling/flux-build/internal/oci"
	"github.com/drone/envsubst"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"github.com/fluxcd/pkg/runtime/transform"
	sourcev1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/go-logr/logr"
	helmaction "helm.sh/helm/v4/pkg/action"
	helmcommon "helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader"
	helmgetter "helm.sh/helm/v4/pkg/getter"
	helmpostrenderer "helm.sh/helm/v4/pkg/postrenderer"
	helmreg "helm.sh/helm/v4/pkg/registry"
	release "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/strvals"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"oras.land/oras-go/v2/registry/remote/auth"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/resource"
	"sigs.k8s.io/kustomize/kyaml/resid"
)

type HelmOpts struct {
	APIVersions      []string
	FailFast         bool
	Cache            chartcache.Interface
	KubeVersion      *helmcommon.KubeVersion
	Getters          helmgetter.Providers
	Decoder          runtime.Decoder
	IncludeHelmHooks bool
}

type CacheKey struct {
	Repo string
}

type Helm struct {
	cache     chartcache.Interface
	Logger    logr.Logger
	opts      HelmOpts
	repoCache *memcache.Cache[CacheKey]
}

func NewHelmBuilder(logger logr.Logger, opts HelmOpts) *Helm {
	if opts.Getters == nil {
		opts.Getters = helmgetter.Providers{
			helmgetter.Provider{
				Schemes: []string{"http", "https"},
				New:     helmgetter.NewHTTPGetter,
			},
			helmgetter.Provider{
				Schemes: []string{"oci"},
				New:     helmgetter.NewOCIGetter,
			},
		}
	}

	if opts.Decoder == nil {
		scheme := runtime.NewScheme()
		_ = helmv2.AddToScheme(scheme)
		_ = sourcev1.AddToScheme(scheme)
		_ = corev1.AddToScheme(scheme)

		codecFactory := serializer.NewCodecFactory(scheme)
		deserializer := codecFactory.UniversalDeserializer()
		opts.Decoder = deserializer
	}

	return &Helm{
		Logger:    logger,
		opts:      opts,
		cache:     opts.Cache,
		repoCache: memcache.New[CacheKey](),
	}
}

func (h *Helm) Build(ctx context.Context, r *resource.Resource, db map[ref]*resource.Resource) (resmap.ResMap, error) {
	r = r.DeepCopy()
	r.SetGvk(resid.Gvk{
		Group:   helmv2.GroupVersion.Group,
		Version: helmv2.GroupVersion.Version,
		Kind:    helmv2.HelmReleaseKind,
	})

	raw, err := r.AsYAML()
	if err != nil {
		return nil, fmt.Errorf("failed to marshal helmrelease as yaml: %w", err)
	}

	substituted, err := envsubst.EvalEnv(string(raw))
	if err != nil {
		return nil, fmt.Errorf("failed to substitute envs: %w", err)
	}

	obj, _, err := h.opts.Decoder.Decode([]byte(substituted), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed decode resource to helmrelease: %w", err)
	}

	hr, ok := obj.(*helmv2.HelmRelease)
	if !ok {
		return nil, fmt.Errorf("expected type %T", helmv2.HelmRelease{})
	}

	if hr.Spec.Chart == nil {
		if hr.Spec.ChartRef != nil {
			return nil, fmt.Errorf("helmrelease %s/%s uses spec.chartRef, which flux-build does not support; use spec.chart (inline HelmChart template)", hr.Namespace, hr.Name)
		}
		return nil, fmt.Errorf("helmrelease %s/%s: spec.chart is required", hr.Namespace, hr.Name)
	}

	namespace := hr.Spec.Chart.Spec.SourceRef.Namespace
	if len(namespace) == 0 {
		namespace = hr.Namespace
	}
	lookupRef := ref{
		GroupKind: schema.GroupKind{
			Group: sourcev1.GroupVersion.Group,
			Kind:  hr.Spec.Chart.Spec.SourceRef.Kind,
		},
		Name:      hr.Spec.Chart.Spec.SourceRef.Name,
		Namespace: namespace,
	}
	source, ok := db[lookupRef]

	if !ok {
		return nil, fmt.Errorf("no source `%v` found for helmrelease `%s/%s`", lookupRef, hr.GetNamespace(), hr.GetName())
	}

	repository, err := h.getRepository(source)
	if err != nil {
		return nil, err
	}

	chartBuild := &chart.Build{}
	err = h.buildChart(ctx, repository, *hr, chartBuild, db)
	if err != nil {
		return nil, err
	}

	values, err := h.composeValues(ctx, db, *hr)
	if err != nil {
		return nil, err
	}

	release, err := h.renderRelease(ctx, *hr, values, chartBuild)
	if err != nil {
		return nil, err
	}

	ksDir, err := os.MkdirTemp("", "helmrelease")
	if err != nil {
		return nil, err
	}

	err = os.WriteFile(filepath.Join(ksDir, "manifest.yaml"), []byte(release.Manifest), 0644)
	if err != nil {
		return nil, err
	}

	if h.opts.IncludeHelmHooks {
		for i, hook := range release.Hooks {
			err := os.WriteFile(filepath.Join(ksDir, fmt.Sprintf("hook_%d.yaml", i)), []byte(hook.Manifest), 0644)
			if err != nil {
				return nil, err
			}
		}
	}

	return Kustomize(ctx, ksDir)
}

func (h *Helm) getRepository(repository *resource.Resource) (runtime.Object, error) {
	copy := repository.DeepCopy()
	copy.SetGvk(resid.Gvk{
		Group:   sourcev1.GroupVersion.Group,
		Version: sourcev1.GroupVersion.Version,
		Kind:    sourcev1.HelmRepositoryKind,
	})

	b, err := copy.AsYAML()
	if err != nil {
		return nil, fmt.Errorf("failed marshal repository as yaml: %w", err)
	}

	r, _, err := h.opts.Decoder.Decode(b, nil, nil)

	if err != nil {
		return nil, fmt.Errorf("failed to decode into helmrepository: %w", err)
	}

	return r, nil
}

func (h *Helm) buildChart(ctx context.Context, repository runtime.Object, release helmv2.HelmRelease, b *chart.Build, db map[ref]*resource.Resource) error {
	chart := &sourcev1.HelmChart{
		Spec: sourcev1.HelmChartSpec{
			Chart:   release.Spec.Chart.Spec.Chart,
			Version: release.Spec.Chart.Spec.Version,
			SourceRef: sourcev1.LocalHelmChartSourceReference{
				APIVersion: release.Spec.Chart.Spec.SourceRef.APIVersion,
				Kind:       release.Spec.Chart.Spec.SourceRef.Kind,
				Name:       release.Spec.Chart.Spec.SourceRef.Name,
			},
			ValuesFiles: release.Spec.Chart.Spec.ValuesFiles,
			//Verify:      release.Spec.Chart.Spec.Verify,
		},
	}

	switch repository := repository.(type) {
	case *sourcev1.HelmRepository:
		return h.buildFromHelmRepository(ctx, chart, repository, b, db)

	}

	return fmt.Errorf("unsupported chart repository `%T`", repository)
}

func (h *Helm) renderRelease(ctx context.Context, hr helmv2.HelmRelease, values helmcommon.Values, b *chart.Build) (*release.Release, error) {
	chrt, err := loader.Load(b.Path)
	if err != nil {
		return nil, err
	}

	ns := hr.GetReleaseNamespace()
	if ns == "" {
		ns = "default"
	}

	cfg := &helmaction.Configuration{}
	client := helmaction.NewInstall(cfg)
	client.ReleaseName = hr.GetReleaseName()
	client.Namespace = ns
	// This is a fully offline, client-only render (equivalent to `helm template`):
	// no cluster interaction, nothing persisted.
	client.DryRunStrategy = helmaction.DryRunClient

	install := hr.GetInstall()
	client.IncludeCRDs = true
	if install.SkipCRDs || install.CRDs == helmv2.Skip {
		client.IncludeCRDs = false
	}

	client.KubeVersion = h.opts.KubeVersion
	client.Timeout = install.GetTimeout(hr.GetTimeout()).Duration
	client.DisableHooks = install.DisableHooks
	client.DisableOpenAPIValidation = install.DisableOpenAPIValidation
	client.SkipSchemaValidation = install.DisableSchemaValidation
	client.Devel = true
	client.EnableDNS = true

	apiVersions := helmcommon.DefaultVersionSet
	apiVersions = append(apiVersions, h.opts.APIVersions...)
	client.APIVersions = apiVersions

	renderer, err := h.postRenderers(hr)
	if err != nil {
		return nil, err
	}
	client.PostRenderer = renderer

	// If user opted-in to install (or replace) CRDs, install them first.
	var legacyCRDsPolicy = helmv2.Create
	if install.SkipCRDs {
		legacyCRDsPolicy = helmv2.Skip
	}

	_, err = h.validateCRDsPolicy(install.CRDs, legacyCRDsPolicy)
	if err != nil {
		return nil, err
	}

	rel, err := client.RunWithContext(ctx, chrt, values)
	if err != nil {
		return nil, err
	}

	rl, ok := rel.(*release.Release)
	if !ok {
		return nil, fmt.Errorf("expected type %T, got %T", &release.Release{}, rel)
	}

	return rl, nil
}

// Create post renderer instances from HelmRelease and combine them into
// a single combined post renderer.
func (h *Helm) postRenderers(hr helmv2.HelmRelease) (helmpostrenderer.PostRenderer, error) {
	var combinedRenderer = postrenderer.NewCombinedPostRenderer()

	for _, r := range hr.Spec.PostRenderers {
		if r.Kustomize != nil {
			combinedRenderer.AddRenderer(postrenderer.NewPostRendererKustomize(r.Kustomize))
		}
	}
	combinedRenderer.AddRenderer(postrenderer.NewPostRendererOriginLabels(&hr))
	combinedRenderer.AddRenderer(postrenderer.NewPostRendererNamespace(&hr))

	if combinedRenderer.Len() == 0 {
		return nil, nil
	}
	return &combinedRenderer, nil
}

func (h *Helm) validateCRDsPolicy(policy helmv2.CRDsPolicy, defaultValue helmv2.CRDsPolicy) (helmv2.CRDsPolicy, error) {
	switch policy {
	case "":
		return defaultValue, nil
	case helmv2.Skip:
		break
	case helmv2.Create:
		break
	case helmv2.CreateReplace:
		break
	default:
		return policy, fmt.Errorf("invalid CRD policy '%s' defined in field CRDsPolicy, valid values are '%s', '%s' or '%s'",
			policy, helmv2.Skip, helmv2.Create, helmv2.CreateReplace,
		)
	}
	return policy, nil
}

// composeValues attempts to resolve all ValuesReference resources
// and merges them as defined. Referenced resources are only retrieved once
// to ensure a single version is taken into account during the merge.
func (h *Helm) composeValues(_ context.Context, db map[ref]*resource.Resource, hr helmv2.HelmRelease) (helmcommon.Values, error) {
	result := helmcommon.Values{}

	for _, v := range hr.Spec.ValuesFrom {
		namespacedName := types.NamespacedName{Namespace: hr.Namespace, Name: v.Name}
		var valuesData []byte

		lookupRef := ref{
			GroupKind: schema.GroupKind{
				Group: "",
				Kind:  v.Kind,
			},
			Name:      v.Name,
			Namespace: hr.Namespace,
		}
		res, ok := db[lookupRef]
		if !ok {
			if !v.Optional {
				return nil, fmt.Errorf("could not find values `%s.%s/%v` for helmrelease `%s/%s`", v.Kind, hr.GetNamespace(), v.Name, hr.GetNamespace(), hr.GetName())
			} else {
				continue
			}
		}

		res.SetGvk(resid.Gvk{
			Group:   "",
			Version: "v1",
			Kind:    v.Kind,
		})

		raw, err := res.AsYAML()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal as yaml: %w", err)
		}

		obj, _, err := h.opts.Decoder.Decode(raw, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed decode values as `v1.%s`: %w", v.Kind, err)
		}

		switch obj := obj.(type) {
		case *corev1.ConfigMap:
			if data, ok := obj.Data[v.GetValuesKey()]; !ok {
				return nil, fmt.Errorf("missing key '%s' in %s '%s'", v.GetValuesKey(), v.Kind, namespacedName)
			} else {
				valuesData = []byte(data)
			}
		case *corev1.Secret:
			if data, ok := obj.Data[v.GetValuesKey()]; ok {
				valuesData = data
			} else if data, ok := obj.StringData[v.GetValuesKey()]; ok {
				valuesData = []byte(data)
			} else {
				return nil, fmt.Errorf("missing key '%s' in %s '%s'", v.GetValuesKey(), v.Kind, namespacedName)
			}
		default:
			return nil, fmt.Errorf("unsupported ValuesReference kind '%s'", v.Kind)
		}

		switch v.TargetPath {
		case "":
			values, err := helmcommon.ReadValues(valuesData)
			if err != nil {
				return nil, fmt.Errorf("unable to read values from key '%s' in %s '%s': %w", v.GetValuesKey(), v.Kind, namespacedName, err)
			}
			result = transform.MergeMaps(result, values)
		default:
			// TODO(hidde): this is a bit of hack, as it mimics the way the option string is passed
			// 	to Helm from a CLI perspective. Given the parser is however not publicly accessible
			// 	while it contains all logic around parsing the target path, it is a fair trade-off.
			stringValuesData := string(valuesData)
			const singleQuote = "'"
			const doubleQuote = "\""
			var err error
			if (strings.HasPrefix(stringValuesData, singleQuote) && strings.HasSuffix(stringValuesData, singleQuote)) || (strings.HasPrefix(stringValuesData, doubleQuote) && strings.HasSuffix(stringValuesData, doubleQuote)) {
				stringValuesData = strings.Trim(stringValuesData, singleQuote+doubleQuote)
				singleValue := v.TargetPath + "=" + stringValuesData
				err = strvals.ParseIntoString(singleValue, result)
			} else {
				singleValue := v.TargetPath + "=" + stringValuesData
				err = strvals.ParseInto(singleValue, result)
			}
			if err != nil {
				return nil, fmt.Errorf("unable to merge value from key '%s' in %s '%s' into target path '%s': %w", v.GetValuesKey(), v.Kind, namespacedName, v.TargetPath, err)
			}
		}
	}

	return transform.MergeMaps(result, hr.GetValues()), nil
}

// resolveSecret looks up a Secret by namespace/name in the resolved kustomize
// resource db (flux-build's static, in-memory stand-in for a live API server)
// and decodes it. It returns ok=false (no error) when the Secret is simply not
// present in db, so callers can distinguish "not referenced" from "failed to decode".
func (h *Helm) resolveSecret(namespace, name string, db map[ref]*resource.Resource) (*corev1.Secret, bool, error) {
	lookupRef := ref{
		GroupKind: schema.GroupKind{
			Group: "",
			Kind:  "Secret",
		},
		Name:      name,
		Namespace: namespace,
	}

	res, ok := db[lookupRef]
	if !ok {
		return nil, false, nil
	}

	raw, err := res.AsYAML()
	if err != nil {
		return nil, false, err
	}

	obj, _, err := h.opts.Decoder.Decode(raw, nil, nil)
	if err != nil {
		return nil, false, err
	}

	secret, ok := obj.(*corev1.Secret)
	if !ok {
		return nil, false, fmt.Errorf("expected type %T, got %T", corev1.Secret{}, obj)
	}

	return secret, true, nil
}

// newRepositoryClient builds a controller-runtime fake client.Client seeded with
// the Secret(s) referenced by the HelmRepository (SecretRef/CertSecretRef), so that
// internal/helm/getter.GetClientOpts (which expects a live client.Client to fetch
// Secrets from a cluster) can be reused unmodified against flux-build's in-memory,
// statically-resolved resource db.
func (h *Helm) newRepositoryClient(repo *sourcev1.HelmRepository, db map[ref]*resource.Resource) (client.Client, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	builder := fake.NewClientBuilder().WithScheme(scheme)

	names := map[string]struct{}{}
	if repo.Spec.SecretRef != nil {
		names[repo.Spec.SecretRef.Name] = struct{}{}
	}
	if repo.Spec.CertSecretRef != nil {
		names[repo.Spec.CertSecretRef.Name] = struct{}{}
	}

	for name := range names {
		secret, ok, err := h.resolveSecret(repo.Namespace, name, db)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve secret '%s/%s': %w", repo.Namespace, name, err)
		}
		if ok {
			builder = builder.WithObjects(secret)
		}
	}

	return builder.Build(), nil
}

// buildFromHelmRepository attempts to pull and/or package a Helm chart with
// the specified data from the v1.HelmRepository and v1.HelmChart
// objects.
// In case of a failure it records v1.FetchFailedCondition on the chart
// object, and returns early.
func (h *Helm) buildFromHelmRepository(ctx context.Context, obj *sourcev1.HelmChart,
	repo *sourcev1.HelmRepository, b *chart.Build, db map[ref]*resource.Resource) error {
	// Used to login with the repository declared provider
	ctxTimeout, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	normalizedURL, err := repository.NormalizeURL(repo.Spec.URL)
	if err != nil {
		return fmt.Errorf("failed to normalize url: %w", err)
	}

	// Check if the chart is already in cache first.
	ref := chart.RemoteReference{Name: obj.Spec.Chart, Version: obj.Spec.Version}
	path, chartCacheKey, err := h.cache.GetOrLock(normalizedURL, ref.WithEscapedName())
	if err != nil {
		return err
	}

	defer func() {
		_ = h.cache.SetUnlock(chartCacheKey)
	}()

	_, err = os.Stat(path)
	uncachedChart := os.IsNotExist(err)

	var chartRepo repository.Downloader
	repoCacheKey := CacheKey{Repo: normalizedURL}
	r, ok := h.repoCache.GetOrLock(repoCacheKey)
	if ok && r != nil {
		chartRepo = r.(repository.Downloader)
	}

	defer h.repoCache.SetUnlock(repoCacheKey, chartRepo)

	if chartRepo == nil {
		h.Logger.V(1).Info("using chart repo", "chartrepo", normalizedURL)

		fakeClient, err := h.newRepositoryClient(repo, db)
		if err != nil {
			return err
		}

		clientOpts, err := getter.GetClientOpts(ctxTimeout, fakeClient, repo, normalizedURL)
		if err != nil && !errors.Is(err, getter.ErrDeprecatedTLSConfig) {
			// GetClientOpts eagerly resolves cloud-provider OCI auth (OIDC) when the
			// HelmRepository declares a Provider but no SecretRef. flux-build may run
			// outside of any cloud (e.g. locally, in CI), in which case such auth is
			// expected to be unavailable; fall back to anonymous access instead of
			// failing the whole build, matching flux-build's previous behavior.
			if repo.Spec.SecretRef == nil && repo.Spec.Provider != "" && isSkippableCloudRegistryAuthErr(err) {
				h.Logger.V(1).Info("cloud registry auto-login not available, falling back to anonymous access", "helmrepository", repo.Name, "error", err.Error())
				anonRepo := repo.DeepCopy()
				anonRepo.Spec.Provider = ""
				clientOpts, err = getter.GetClientOpts(ctxTimeout, fakeClient, anonRepo, normalizedURL)
			}

			if err != nil && !errors.Is(err, getter.ErrDeprecatedTLSConfig) {
				return fmt.Errorf("failed to configure Helm client with secret data: %w", err)
			}
		}

		if err != nil && errors.Is(err, getter.ErrDeprecatedTLSConfig) {
			h.Logger.V(1).Info("helmrepository uses a deprecated TLS configuration via spec.secretRef, consider migrating to spec.certSecretRef", "helmrepository", repo.Name)
		}

		var tlsConfig *tls.Config
		getterOpts := []helmgetter.Option{
			helmgetter.WithURL(normalizedURL),
			helmgetter.WithTimeout(1 * time.Minute),
			helmgetter.WithPassCredentialsAll(repo.Spec.PassCredentials),
		}
		if clientOpts != nil {
			getterOpts = clientOpts.GetterOpts
			tlsConfig = clientOpts.TLSConfig
		}

		// Initialize the chart repository
		switch repo.Spec.Type {
		case sourcev1.HelmRepositoryTypeOCI:
			if !helmreg.IsOCI(normalizedURL) {
				return fmt.Errorf("invalid OCI registry URL: %s", normalizedURL)
			}

			var ociAuth auth.CredentialFunc
			if clientOpts != nil {
				ociAuth = clientOpts.OCIAuth
			}

			registryClient, err := registry.NewClient(ociAuth, tlsConfig, repo.Spec.Insecure)
			if err != nil {
				return fmt.Errorf("failed to construct Helm client: %w", err)
			}

			var verifiers []soci.Verifier
			/*if obj.Spec.Verify != nil {
				provider := obj.Spec.Verify.Provider
				verifiers, err = h.makeVerifiers(ctx, obj, authenticator, keychain)
				if err != nil {
					if obj.Spec.Verify.SecretRef == nil {
						provider = fmt.Sprintf("%s keyless", provider)
					}
					return fmt.Errorf("failed to verify the signature using provider '%s': %w", provider, err)
				}
			}*/

			// Tell the chart repository to use the OCI client with the configured getter
			getterOpts = append(getterOpts, helmgetter.WithRegistryClient(registryClient))
			chartRepoOpts := []repository.OCIChartRepositoryOption{
				repository.WithOCIGetter(h.opts.Getters),
				repository.WithOCIGetterOptions(getterOpts),
				repository.WithOCIRegistryClient(registryClient),
				repository.WithVerifiers(verifiers),
			}
			if repo.Spec.Insecure {
				chartRepoOpts = append(chartRepoOpts, repository.WithInsecureHTTP())
			}

			ociChartRepo, err := repository.NewOCIChartRepository(normalizedURL, chartRepoOpts...)
			if err != nil {
				return err
			}
			chartRepo = ociChartRepo
		default:
			httpChartRepo, err := repository.NewChartRepository(normalizedURL, os.TempDir(), h.opts.Getters, tlsConfig, getterOpts...)
			if err != nil {
				return err
			}

			chartRepo = httpChartRepo
		}
	}

	// Construct the chart builder with scoped configuration
	cb := chart.NewRemoteBuilder(chartRepo)
	opts := chart.BuildOptions{
		ValuesFiles: obj.GetValuesFiles(),
		//Force:       obj.Generation != obj.Status.ObservedGeneration,
		// The remote builder will not attempt to download the chart if
		// an artifact exists with the same name and version and `Force` is false.
		// It will however try to verify the chart if `obj.Spec.Verify` is set, at every reconciliation.
		Verify: obj.Spec.Verify != nil && obj.Spec.Verify.Provider != "",
	}

	if !uncachedChart {
		opts.CachedChart = path
		h.Logger.V(1).Info("using cached chart artifact", "chart", ref.String(), "path", path)
	}

	// Set the VersionMetadata to the object's Generation if ValuesFiles is defined
	// This ensures changes can be noticed by the Artifact consumer
	if len(opts.GetValuesFiles()) > 0 {
		opts.VersionMetadata = strconv.FormatInt(obj.Generation, 10)
	}

	// Build the chart
	build, err := cb.Build(ctx, ref, path, opts)
	if err != nil {
		return err
	}

	if uncachedChart {
		h.Logger.V(1).Info("cached new chart", "chart", ref.String(), "path", path)
	}

	*b = *build
	return nil
}

func isSkippableCloudRegistryAuthErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "AWS_REGION environment variable is not set"):
		return true
	case strings.Contains(msg, "could not find default credentials"):
		return true
	case strings.Contains(msg, "DefaultAzureCredential authentication failed"):
		return true
	}
	return false
}
