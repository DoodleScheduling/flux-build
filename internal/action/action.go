package action

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/alitto/pond/v2"
	"github.com/doodlescheduling/flux-build/internal/build"
	chartcache "github.com/doodlescheduling/flux-build/internal/helm/chart/cache"
	helmv2 "github.com/fluxcd/helm-controller/api/v2"
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

// submit forwards task panics (captured by pond) to errs, matching pre-pond-v2 PanicHandler behavior.
func submit(p pond.Pool, task func(), errs chan<- error, panicForward *sync.WaitGroup) {
	fut := p.Submit(task)
	panicForward.Add(1)
	go func() {
		defer panicForward.Done()
		if err := fut.Wait(); err != nil && errors.Is(err, pond.ErrPanic) {
			errs <- fmt.Errorf("worker exits from a panic: %w", err)
		}
	}()
}

func (a *Action) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error)
	var panicForward sync.WaitGroup

	var lastErr error
	helmResultPool := pond.NewPool(1, pond.WithContext(ctx))
	kustomizePool := pond.NewPool(len(a.Paths), pond.WithContext(ctx))
	helmPool := pond.NewPool(a.Workers, pond.WithContext(ctx))
	resourcePool := pond.NewPool(1, pond.WithContext(ctx))

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

	submit(helmResultPool, func() {
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
	}, errs, &panicForward)

	for _, path := range a.Paths {
		p := path
		a.Logger.Info("build kustomize path", "path", p)

		submit(kustomizePool, func() {
			if index, err := build.Kustomize(ctx, p); err != nil {
				a.Logger.Error(err, "failed build kustomization", "path", p)
				errs <- err
			} else {
				manifests <- index
				resources <- index
			}
		}, errs, &panicForward)
	}

	index := make(build.ResourceIndex)
	submit(resourcePool, func() {
		for build := range resources {
			if err := index.Push(build.Resources()); err != nil {
				errs <- err
				continue
			}
		}
	}, errs, &panicForward)

	kustomizePool.StopAndWait()
	close(resources)
	resourcePool.StopAndWait()

	for _, r := range index {
		res := r
		if r.GetKind() != helmv2.HelmReleaseKind {
			continue
		}

		if ctx.Err() != nil {
			break
		}

		submit(helmPool, func() {
			a.Logger.Info("build helm release", "namespace", res.GetNamespace(), "name", res.GetName())
			index, err := helmBuilder.Build(ctx, res, index)
			if err != nil {
				a.Logger.Error(err, "failed build helmrelease", "namespace", res.GetNamespace(), "name", res.GetName())
				errs <- err
				return
			}

			manifests <- index
		}, errs, &panicForward)
	}

	helmPool.StopAndWait()
	close(manifests)
	helmResultPool.StopAndWait()
	panicForward.Wait()
	close(errs)

	return nil
}
