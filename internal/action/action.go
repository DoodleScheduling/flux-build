package action

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/doodlescheduling/flux-kustomize-action/internal/build"
	"github.com/doodlescheduling/flux-kustomize-action/internal/worker"
	helmv1 "github.com/fluxcd/helm-controller/api/v2beta1"
	"github.com/sethvargo/go-githubactions"
	"helm.sh/helm/v3/pkg/chartutil"
)

type Action struct {
	AllowFailure bool
	FailFast     bool
	Workers      int
	CacheDir     string
	Paths        []string
	KubeVersion  *chartutil.KubeVersion
	Action       *githubactions.Action
	Logger       *log.Logger
}

func NewFromInputs(ctx context.Context, action *githubactions.Action) (*Action, error) {
	paths := githubactions.GetInput("paths")
	if paths == "" {
		paths = "."
	}

	workers := 1
	if githubactions.GetInput("workers") != "" {
		v, err := strconv.Atoi(githubactions.GetInput("workers"))
		if err == nil {
			workers = v
		}
	}

	failFast := false
	if githubactions.GetInput("fail-fast") != "" {
		v, err := strconv.ParseBool(githubactions.GetInput("fail-fast"))
		if err == nil {
			failFast = v
		}
	}

	allowFailure := false
	if githubactions.GetInput("allow-failure") != "" {
		v, err := strconv.ParseBool(githubactions.GetInput("allow-failure"))
		if err == nil {
			allowFailure = v
		}
	}

	kubeVersion := &chartutil.KubeVersion{
		Major:   "1",
		Minor:   "27",
		Version: "1.27.0",
	}

	if githubactions.GetInput("kube-version") != "" {
		v, err := chartutil.ParseKubeVersion(githubactions.GetInput("kube-version"))
		if err != nil {
			return nil, err
		}

		kubeVersion = v
	}

	a := Action{
		FailFast:     failFast,
		AllowFailure: allowFailure,
		Workers:      workers,
		CacheDir:     githubactions.GetInput("cache-dir"),
		KubeVersion:  kubeVersion,
		Paths:        strings.Split(paths, ","),
		Action:       action,
		Logger:       log.New(os.Stdout, "", 0),
	}

	return &a, nil
}

func (a *Action) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	helmResultPool := worker.NewPool(
		worker.PoolOptions{
			Workers: len(a.Paths),
		},
	).Start(ctx)

	kustomizePool := worker.NewPool(
		worker.PoolOptions{
			Workers: len(a.Paths),
		},
	).Start(ctx)

	helmPool := worker.NewPool(
		worker.PoolOptions{
			Workers: a.Workers,
		},
	).Start(ctx)

	manifests := make(chan []byte, len(a.Paths))
	helmBuilder := build.NewHelmBuilder(build.HelmOpts{
		KubeVersion: a.KubeVersion,
	})

	for _, path := range a.Paths {
		p := path
		a.Action.Infof("build kustomize path `%s`", p)
		kustomizePool.Push(worker.Task(func(ctx context.Context) error {
			k := build.NewKustomizeBuilder(build.KustomizeOpts{
				Path: p,
			})

			if err := k.Build(ctx); err != nil {
				a.Action.Errorf("failed build kustomization `%s`: %s", p, err.Error())
				if a.FailFast {
					cancel()
				}

				return err
			}

			a.Action.Infof("kustomization build for `%s` at `%s`", p, k.File.Name())

			helmResultPool.Push(worker.Task(func(ctx context.Context) error {
				for {
					select {
					case <-ctx.Done():
						return nil
					case manifest, ok := <-manifests:
						if !ok {
							return nil
						}

						_, err := k.Write(manifest)
						if err != nil {
							a.Action.Errorf("failed to write helm manifests to output: %s", err.Error())
							if a.FailFast {
								cancel()
							}

							return err
						}
					}
				}
			}))

			for _, r := range k.Resources() {
				res := r
				if r.GetKind() != helmv1.HelmReleaseKind {
					continue
				}

				helmPool.Push(worker.Task(func(ctx context.Context) error {
					a.Action.Infof("build helm release %s/%s", res.GetNamespace(), res.GetName())

					manifest, err := helmBuilder.Build(ctx, res, k)
					if err != nil {
						a.Action.Errorf("failed build helmrelease `%s/%s`: %s", res.GetNamespace(), res.GetName(), err.Error())
						if a.FailFast {
							cancel()
						}

						return err
					}

					if ctx.Err() != nil {
						return nil
					}

					manifests <- manifest
					return nil
				}))
			}

			return nil
		}))
	}

	a.exit(kustomizePool, helmPool)
	cancel()
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
