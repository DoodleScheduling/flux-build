package action

import (
	"context"
	"io"
	"os"

	"github.com/doodlescheduling/flux-build/internal/build"
	"github.com/doodlescheduling/flux-build/internal/cachemgr"
	"github.com/doodlescheduling/flux-build/internal/worker"
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
	Cache            *cachemgr.Cache
	Paths            []string
	APIVersions      []string
	IncludeHelmHooks bool
	KubeVersion      *chartutil.KubeVersion
	Logger           logr.Logger
}

func (a *Action) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	abort := func(err error) error {
		if err == nil {
			return nil
		}

		if a.FailFast {
			cancel()
		}

		return err
	}

	helmResultPool := worker.New(ctx, worker.PoolOptions{
		Workers: 1,
	})

	kustomizePool := worker.New(ctx, worker.PoolOptions{
		Workers: len(a.Paths),
	})

	helmPool := worker.New(ctx, worker.PoolOptions{
		Workers: a.Workers,
	})

	resourcePool := worker.New(ctx, worker.PoolOptions{
		Workers: 1,
	})

	resources := make(chan resmap.ResMap, len(a.Paths))
	manifests := make(chan resmap.ResMap, a.Workers)
	helmBuilder := build.NewHelmBuilder(a.Logger, build.HelmOpts{
		APIVersions:      a.APIVersions,
		KubeVersion:      a.KubeVersion,
		IncludeHelmHooks: a.IncludeHelmHooks,
		Cache:            a.Cache,
	})

	helmResultPool.Push(worker.Task(func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case index, ok := <-manifests:
				if !ok {
					return nil
				}

				y, err := index.AsYaml()
				if err != nil {
					a.Logger.Error(err, "failed to encode as yaml")
					return abort(err)
				}

				_, err = a.Output.Write(append([]byte("---\n"), y...))
				if err != nil {

					a.Logger.Error(err, "failed to write helm manifests to output")
					return abort(err)
				}
			}
		}
	}))

	for _, path := range a.Paths {
		p := path
		a.Logger.Info("build kustomize path", "path", p)

		kustomizePool.Push(worker.Task(func(ctx context.Context) error {
			if index, err := build.Kustomize(ctx, p); err != nil {
				a.Logger.Error(err, "failed build kustomization", "path", p)
				return abort(err)
			} else {
				manifests <- index
				resources <- index
			}

			return nil
		}))
	}

	index := make(build.ResourceIndex)
	resourcePool.Push(worker.Task(func(ctx context.Context) error {
		for {
			select {
			case <-ctx.Done():
				return nil
			case build, ok := <-resources:
				if !ok {
					return nil
				}

				if err := index.Push(build.Resources()); err != nil {
					return abort(err)
				}

				if ctx.Err() != nil {
					return nil
				}
			}
		}
	}))

	a.exit(kustomizePool)
	close(resources)
	a.exit(resourcePool)

	for _, r := range index {
		res := r
		if r.GetKind() != helmv1.HelmReleaseKind {
			continue
		}

		helmPool.Push(worker.Task(func(ctx context.Context) error {
			a.Logger.Info("build helm release", "namespace", res.GetNamespace(), "name", res.GetName())
			index, err := helmBuilder.Build(ctx, res, index)
			if err != nil {
				a.Logger.Error(err, "failed build helmrelease", "namespace", res.GetNamespace(), "name", res.GetName())
				return abort(err)
			}

			if ctx.Err() != nil {
				return nil
			}

			manifests <- index
			return nil
		}))
	}

	a.exit(helmPool)
	close(manifests)
	a.exit(helmResultPool)

	return nil
}

func (a *Action) exit(waiters ...worker.Waiter) {
	for _, w := range waiters {
		err := w.Wait()
		if err != nil && !a.AllowFailure {
			os.Exit(1)
		}
	}
}
