package action

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/alitto/pond/v2"
	"github.com/doodlescheduling/flux-build/internal/build"
	chartcache "github.com/doodlescheduling/flux-build/internal/helm/chart/cache"
	helmv1 "github.com/fluxcd/helm-controller/api/v2beta1"
	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/chartutil"
	"sigs.k8s.io/kustomize/api/resmap"
)

type Action struct {
	Output           io.Writer
	AllowFailure     bool
	FailFast         bool
	Workers          int
	Cache            chartcache.Interface
	Paths            []string
	APIVersions      []string
	IncludeHelmHooks bool
	KubeVersion      *chartutil.KubeVersion
	Logger           logr.Logger
}

func (a *Action) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error)
	panicHandler := func(panic interface{}) {
		errs <- fmt.Errorf("worker exits from a panic: %v\nStack trace: %s\n", panic, string(debug.Stack()))
	}

	var lastErr error
	helmResultPool := pond.New(1, 1, pond.Context(ctx), pond.PanicHandler(panicHandler))
	kustomizePool := pond.New(len(a.Paths), len(a.Paths), pond.Context(ctx), pond.PanicHandler(panicHandler))
	helmPool := pond.New(a.Workers, a.Workers, pond.Context(ctx), pond.PanicHandler(panicHandler))
	resourcePool := pond.New(1, 1, pond.Context(ctx), pond.PanicHandler(panicHandler))

	defer func() {
		if lastErr != nil && !a.AllowFailure {
			fmt.Fprintln(os.Stderr, lastErr.Error())
			os.Exit(1)
		}
	}()

	go func() {
		for err := range errs {
			if err == nil {
				continue
			}

			lastErr = err

			if a.FailFast {
				cancel()
			}
		}
	}()

	resources := make(chan resmap.ResMap, len(a.Paths))
	manifests := make(chan resmap.ResMap, a.Workers)
	helmBuilder := build.NewHelmBuilder(a.Logger, build.HelmOpts{
		APIVersions:      a.APIVersions,
		KubeVersion:      a.KubeVersion,
		IncludeHelmHooks: a.IncludeHelmHooks,
		Cache:            a.Cache,
	})

	helmResultPool.Submit(func() {
		for index := range manifests {
			y, err := index.AsYaml()
			if err != nil {
				a.Logger.Error(err, "failed to encode as yaml")
				errs <- err
				continue
			}

			_, err = a.Output.Write(append([]byte("---\n"), y...))
			if err != nil {
				a.Logger.Error(err, "failed to write helm manifests to output")
				errs <- err
				continue
			}
		}
	})

	for _, path := range a.Paths {
		p := path
		a.Logger.Info("build kustomize path", "path", p)

		kustomizePool.Submit(func() {
			if index, err := build.Kustomize(ctx, p); err != nil {
				a.Logger.Error(err, "failed build kustomization", "path", p)
				errs <- err
			} else {
				manifests <- index
				resources <- index
			}
		})
	}

	index := make(build.ResourceIndex)
	resourcePool.Submit(func() {
		for build := range resources {
			if err := index.Push(build.Resources()); err != nil {
				errs <- err
				continue
			}
		}
	})

	kustomizePool.StopAndWait()
	close(resources)
	resourcePool.StopAndWait()

	for _, r := range index {
		res := r
		if r.GetKind() != helmv1.HelmReleaseKind {
			continue
		}

		if ctx.Err() != nil {
			break
		}

		helmPool.Submit(func() {
			a.Logger.Info("build helm release", "namespace", res.GetNamespace(), "name", res.GetName())
			index, err := helmBuilder.Build(ctx, res, index)
			if err != nil {
				a.Logger.Error(err, "failed build helmrelease", "namespace", res.GetNamespace(), "name", res.GetName())
				errs <- err
				return
			}

			manifests <- index
		})
	}

	helmPool.StopAndWait()
	close(manifests)
	helmResultPool.StopAndWait()
	close(errs)

	return nil
}
