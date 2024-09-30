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
	helmv2beta1 "github.com/fluxcd/helm-controller/api/v2beta1"
	"github.com/fluxcd/pkg/oci"
	"github.com/fluxcd/pkg/oci/auth/login"
	"github.com/fluxcd/pkg/runtime/transform"
	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/go-logr/logr"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	helmaction "helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	helmgetter "helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/postrender"
	helmreg "helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/release"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/helm/pkg/strvals"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/resource"
	"sigs.k8s.io/kustomize/kyaml/resid"
)

type HelmOpts struct {
	APIVersions      []string
	FailFast         bool
	Cache            chartcache.Interface
	KubeVersion      *chartutil.KubeVersion
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
		_ = helmv2beta1.AddToScheme(scheme)
		_ = sourcev1beta2.AddToScheme(scheme)
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
		Group:   helmv2beta1.GroupVersion.Group,
		Version: helmv2beta1.GroupVersion.Version,
		Kind:    helmv2beta1.HelmReleaseKind,
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

	hr, ok := obj.(*helmv2beta1.HelmRelease)
	if !ok {
		return nil, fmt.Errorf("expected type %T", helmv2beta1.HelmRelease{})
	}

	namespace := hr.Spec.Chart.Spec.SourceRef.Namespace
	if len(namespace) == 0 {
		namespace = hr.ObjectMeta.Namespace
	}
	lookupRef := ref{
		GroupKind: schema.GroupKind{
			Group: sourcev1beta2.GroupVersion.Group,
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
		Group:   sourcev1beta2.GroupVersion.Group,
		Version: sourcev1beta2.GroupVersion.Version,
		Kind:    sourcev1beta2.HelmRepositoryKind,
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

func (h *Helm) buildChart(ctx context.Context, repository runtime.Object, release helmv2beta1.HelmRelease, b *chart.Build, db map[ref]*resource.Resource) error {
	chart := &sourcev1beta2.HelmChart{
		Spec: sourcev1beta2.HelmChartSpec{
			Chart:   release.Spec.Chart.Spec.Chart,
			Version: release.Spec.Chart.Spec.Version,
			SourceRef: sourcev1beta2.LocalHelmChartSourceReference{
				APIVersion: release.Spec.Chart.Spec.SourceRef.APIVersion,
				Kind:       release.Spec.Chart.Spec.SourceRef.Kind,
				Name:       release.Spec.Chart.Spec.SourceRef.Name,
			},
			ValuesFiles: release.Spec.Chart.Spec.ValuesFiles,
			ValuesFile:  release.Spec.Chart.Spec.ValuesFile,
			//Verify:      release.Spec.Chart.Spec.Verify,
		},
	}

	switch repository := repository.(type) {
	case *sourcev1beta2.HelmRepository:
		return h.buildFromHelmRepository(ctx, chart, repository, b, db)

	}

	return fmt.Errorf("unsupported chart repository `%T`", repository)
}

func (h *Helm) renderRelease(ctx context.Context, hr helmv2beta1.HelmRelease, values chartutil.Values, b *chart.Build) (*release.Release, error) {
	chart, err := loader.Load(b.Path)
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
	client.DryRun = true

	client.IncludeCRDs = true
	if hr.Spec.Install != nil && (hr.Spec.Install.SkipCRDs || hr.Spec.Install.CRDs == helmv2beta1.Skip) {
		client.IncludeCRDs = false
	}

	client.KubeVersion = h.opts.KubeVersion
	client.ClientOnly = true
	client.Timeout = hr.Spec.GetInstall().GetTimeout(hr.GetTimeout()).Duration
	client.DisableHooks = hr.Spec.GetInstall().DisableHooks
	client.DisableOpenAPIValidation = hr.Spec.GetInstall().DisableOpenAPIValidation
	client.Devel = true
	client.EnableDNS = true

	apiVersions := chartutil.DefaultVersionSet
	apiVersions = append(apiVersions, h.opts.APIVersions...)
	client.APIVersions = apiVersions

	renderer, err := h.postRenderers(hr)
	if err != nil {
		return nil, err
	}
	client.PostRenderer = renderer

	// If user opted-in to install (or replace) CRDs, install them first.
	var legacyCRDsPolicy = helmv2beta1.Create
	if hr.Spec.GetInstall().SkipCRDs {
		legacyCRDsPolicy = helmv2beta1.Skip
	}

	_, err = h.validateCRDsPolicy(hr.Spec.GetInstall().CRDs, legacyCRDsPolicy)
	if err != nil {
		return nil, err
	}

	return client.RunWithContext(ctx, chart, values)
}

// Create post renderer instances from HelmRelease and combine them into
// a single combined post renderer.
func (h *Helm) postRenderers(hr helmv2beta1.HelmRelease) (postrender.PostRenderer, error) {
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

func (h *Helm) validateCRDsPolicy(policy helmv2beta1.CRDsPolicy, defaultValue helmv2beta1.CRDsPolicy) (helmv2beta1.CRDsPolicy, error) {
	switch policy {
	case "":
		return defaultValue, nil
	case helmv2beta1.Skip:
		break
	case helmv2beta1.Create:
		break
	case helmv2beta1.CreateReplace:
		break
	default:
		return policy, fmt.Errorf("invalid CRD policy '%s' defined in field CRDsPolicy, valid values are '%s', '%s' or '%s'",
			policy, helmv2beta1.Skip, helmv2beta1.Create, helmv2beta1.CreateReplace,
		)
	}
	return policy, nil
}

// composeValues attempts to resolve all v2beta1.ValuesReference resources
// and merges them as defined. Referenced resources are only retrieved once
// to ensure a single version is taken into account during the merge.
func (h *Helm) composeValues(_ context.Context, db map[ref]*resource.Resource, hr helmv2beta1.HelmRelease) (chartutil.Values, error) {
	result := chartutil.Values{}

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
			values, err := chartutil.ReadValues(valuesData)
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

func (h *Helm) getHelmRepositorySecret(repository *sourcev1beta2.HelmRepository, db map[ref]*resource.Resource) (*corev1.Secret, error) {
	if repository.Spec.SecretRef == nil {
		return nil, nil
	}

	lookupRef := ref{
		GroupKind: schema.GroupKind{
			Group: "",
			Kind:  "Secret",
		},
		Name:      repository.Spec.SecretRef.Name,
		Namespace: repository.ObjectMeta.Namespace,
	}

	if secret, ok := db[lookupRef]; ok {
		raw, err := secret.AsYAML()
		if err != nil {
			return nil, err
		}

		obj, _, err := h.opts.Decoder.Decode(raw, nil, nil)
		if err != nil {
			return nil, err
		}

		return obj.(*corev1.Secret), nil
	}

	return nil, fmt.Errorf("no repository secret `%v` found for helmrepository %s/%s", lookupRef, repository.Namespace, repository.Name)
}

func (h *Helm) clientOptionsFromSecret(secret *corev1.Secret, normalizedURL string) ([]helmgetter.Option, *tls.Config, error) {
	opts, err := getter.ClientOptionsFromSecret(*secret)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to configure Helm client with secret data: %w", err)
	}

	tlsConfig, err := getter.TLSClientConfigFromSecret(*secret, normalizedURL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create TLS client config with secret data: %w", err)
	}

	return opts, tlsConfig, nil
}

// buildFromHelmRepository attempts to pull and/or package a Helm chart with
// the specified data from the v1beta2.HelmRepository and v1beta2.HelmChart
// objects.
// In case of a failure it records v1beta2.FetchFailedCondition on the chart
// object, and returns early.
func (h *Helm) buildFromHelmRepository(ctx context.Context, obj *sourcev1beta2.HelmChart,
	repo *sourcev1beta2.HelmRepository, b *chart.Build, db map[ref]*resource.Resource) error {
	var (
		tlsConfig     *tls.Config
		authenticator authn.Authenticator
		keychain      authn.Keychain
	)

	// Used to login with the repository declared provider
	ctxTimeout, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()

	normalizedURL, err := repository.NormalizeURL(repo.Spec.URL)
	if err != nil {
		return fmt.Errorf("failed to normalize url: %w", err)
	}

	var chartRepo repository.Downloader
	repoCacheKey := CacheKey{Repo: normalizedURL}
	r, ok := h.repoCache.GetOrLock(repoCacheKey)
	if ok {
		chartRepo = r.(repository.Downloader)
	}

	defer h.repoCache.SetUnlock(repoCacheKey, nil)

	if chartRepo == nil {
		h.Logger.V(1).Info("using chart repo", "chartrepo", normalizedURL)

		// Construct the Getter options from the HelmRepository data
		clientOpts := []helmgetter.Option{
			helmgetter.WithURL(normalizedURL),
			helmgetter.WithTimeout(1 * time.Minute),
			helmgetter.WithPassCredentialsAll(repo.Spec.PassCredentials),
		}

		if secret, err := h.getHelmRepositorySecret(repo, db); secret != nil || err != nil {
			if err != nil {
				return err
			}

			// Build client options from secret
			opts, tlsCfg, err := h.clientOptionsFromSecret(secret, normalizedURL)
			if err != nil {
				return err
			}
			clientOpts = append(clientOpts, opts...)
			tlsConfig = tlsCfg

			// Build registryClient options from secret
			keychain, err = registry.LoginOptionFromSecret(normalizedURL, *secret)
			if err != nil {
				return fmt.Errorf("failed to configure Helm client with secret data: %w", err)
			}
		} else if repo.Spec.Provider != sourcev1beta2.GenericOCIProvider && repo.Spec.Type == sourcev1beta2.HelmRepositoryTypeOCI {
			auth, authErr := oidcAuth(ctxTimeout, repo.Spec.URL, repo.Spec.Provider)
			if authErr != nil && !errors.Is(authErr, oci.ErrUnconfiguredProvider) {
				return fmt.Errorf("failed to get credential from %s: %w", repo.Spec.Provider, authErr)
			}
			if auth != nil {
				authenticator = auth
			}
		}

		loginOpt, err := makeLoginOption(authenticator, keychain, normalizedURL)
		if err != nil {
			return err
		}

		// Initialize the chart repository
		switch repo.Spec.Type {
		case sourcev1beta2.HelmRepositoryTypeOCI:
			if !helmreg.IsOCI(normalizedURL) {
				return fmt.Errorf("invalid OCI registry URL: %s", normalizedURL)
			}

			// with this function call, we create a temporary file to store the credentials if needed.
			// this is needed because otherwise the credentials are stored in ~/.docker/config.json.
			// TODO@souleb: remove this once the registry move to Oras v2
			// or rework to enable reusing credentials to avoid the unneccessary handshake operations
			registryClient, _, err := registry.ClientGenerator(loginOpt != nil)
			if err != nil {
				return fmt.Errorf("failed to construct Helm client: %w", err)
			}

			/*if credentialsFile != "" {
				defer func() {
					if err := os.Remove(credentialsFile); err != nil {
						//r.eventLogf(ctx, obj, corev1.EventTypeWarning, meta.FailedReason,
						//		"failed to delete temporary credentials file: %s", err)
					}
				}()
			}*/

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
			clientOpts = append(clientOpts, helmgetter.WithRegistryClient(registryClient))
			ociChartRepo, err := repository.NewOCIChartRepository(normalizedURL,
				repository.WithOCIGetter(h.opts.Getters),
				repository.WithOCIGetterOptions(clientOpts),
				repository.WithOCIRegistryClient(registryClient),
				repository.WithVerifiers(verifiers))
			if err != nil {
				return err
			}
			chartRepo = ociChartRepo

			// If login options are configured, use them to login to the registry
			// The OCIGetter will later retrieve the stored credentials to pull the chart
			if loginOpt != nil {
				err = ociChartRepo.Login(loginOpt)
				if err != nil {
					return fmt.Errorf("failed to login to OCI registry: %w", err)
				}
			}
		default:
			httpChartRepo, err := repository.NewChartRepository(normalizedURL, os.TempDir(), h.opts.Getters, tlsConfig, clientOpts...)
			if err != nil {
				return err
			}

			chartRepo = httpChartRepo
		}

		h.repoCache.SetUnlock(repoCacheKey, chartRepo)
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

	ref := chart.RemoteReference{Name: obj.Spec.Chart, Version: obj.Spec.Version}
	path, chartCacheKey, err := h.cache.GetOrLock(normalizedURL, ref)
	if err != nil {
		return err
	}

	defer func() {
		_ = h.cache.SetUnlock(chartCacheKey)
	}()

	_, err = os.Stat(path)
	newItem := os.IsNotExist(err)

	if !newItem {
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

	if newItem {
		h.Logger.V(1).Info("cached new chart", "chart", ref.String(), "path", path)
	}

	*b = *build
	return nil
}

// oidcAuth generates the OIDC credential authenticator based on the specified cloud provider.
func oidcAuth(ctx context.Context, url, provider string) (authn.Authenticator, error) {
	u := strings.TrimPrefix(url, sourcev1beta2.OCIRepositoryPrefix)
	ref, err := name.ParseReference(u)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL '%s': %w", u, err)
	}

	opts := login.ProviderOptions{}
	switch provider {
	case sourcev1beta2.AmazonOCIProvider:
		opts.AwsAutoLogin = true
	case sourcev1beta2.AzureOCIProvider:
		opts.AzureAutoLogin = true
	case sourcev1beta2.GoogleOCIProvider:
		opts.GcpAutoLogin = true
	}

	return login.NewManager().Login(ctx, u, ref, opts)
}

// makeLoginOption returns a registry login option for the given HelmRepository.
// If the HelmRepository does not specify a secretRef, a nil login option is returned.
func makeLoginOption(auth authn.Authenticator, keychain authn.Keychain, registryURL string) (helmreg.LoginOption, error) {
	if auth != nil {
		return registry.AuthAdaptHelper(auth)
	}

	if keychain != nil {
		return registry.KeychainAdaptHelper(keychain)(registryURL)
	}

	return nil, nil
}

// makeVerifiers returns a list of verifiers for the given chart.
/*func (h *Helm) makeVerifiers(ctx context.Context, obj *sourcev1beta2.HelmChart, auth authn.Authenticator, keychain authn.Keychain) ([]soci.Verifier, error) {
	var verifiers []soci.Verifier
	verifyOpts := []remote.Option{}
	if auth != nil {
		verifyOpts = append(verifyOpts, remote.WithAuth(auth))
	} else {
		verifyOpts = append(verifyOpts, remote.WithAuthFromKeychain(keychain))
	}

	switch obj.Spec.Verify.Provider {
	case "cosign":
		defaultCosignOciOpts := []soci.Options{
			soci.WithRemoteOptions(verifyOpts...),
		}

		// get the public keys from the given secret
		if secretRef := obj.Spec.Verify.SecretRef; secretRef != nil {
			certSecretName := types.NamespacedName{
				Namespace: obj.Namespace,
				Name:      secretRef.Name,
			}

			var pubSecret corev1.Secret
			if err := h.Get(ctx, certSecretName, &pubSecret); err != nil {
				return nil, err
			}

			for k, data := range pubSecret.Data {
				// search for public keys in the secret
				if strings.HasSuffix(k, ".pub") {
					verifier, err := soci.NewCosignVerifier(ctx, append(defaultCosignOciOpts, soci.WithPublicKey(data))...)
					if err != nil {
						return nil, err
					}
					verifiers = append(verifiers, verifier)
				}
			}

			if len(verifiers) == 0 {
				return nil, fmt.Errorf("no public keys found in secret '%s'", certSecretName)
			}
			return verifiers, nil
		}

		// if no secret is provided, add a keyless verifier
		verifier, err := soci.NewCosignVerifier(ctx, defaultCosignOciOpts...)
		if err != nil {
			return nil, err
		}
		verifiers = append(verifiers, verifier)
		return verifiers, nil
	default:
		return nil, fmt.Errorf("unsupported verification provider: %s", obj.Spec.Verify.Provider)
	}
}
*/
