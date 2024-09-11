package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/doodlescheduling/flux-build/internal/action"
	"github.com/doodlescheduling/flux-build/internal/cachemgr"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/sethvargo/go-envconfig"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
	"helm.sh/helm/v3/pkg/chartutil"
)

type Config struct {
	Log struct {
		Level    string `env:"LOG_LEVEL, default=info"`
		Encoding string `env:"LOG_ENCODING, default=json"`
	}
	Output           string   `env:"OUTPUT, default=/dev/stdout"`
	FailFast         bool     `env:"FAIL_FAST"`
	IncludeHelmHooks bool     `env:"INCLUDE_HELM_HOOKS"`
	AllowFailure     bool     `env:"ALLOW_FAILURE"`
	Workers          int      `env:"WORKERS"`
	APIVersions      []string `env:"API_VERSIONS"`
	KubeVersion      string   `env:"KUBE_VERSION"`
	CacheEnabled     bool     `env:"CACHE_ENABLED"`
	CacheDir         string   `env:"CACHE_DIR"`
	Cache            string   `env:"CACHE"`
}

var (
	config = &Config{}
)

func getDefaultCacheDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "flux-build")
	}

	return filepath.Join(homeDir, ".cache", "flux-build")
}

func init() {
	flag.StringVarP(&config.Log.Level, "log-level", "l", "", "Define the log level (default is warning) [debug,info,warn,error]")
	flag.StringVarP(&config.Log.Encoding, "log-encoding", "e", "", "Define the log format (default is json) [json,console]")
	flag.StringVarP(&config.Output, "output", "o", "", "Path to output")
	flag.BoolVar(&config.AllowFailure, "allow-failure", false, "Do not exit > 0 if an error occurred")
	flag.BoolVar(&config.IncludeHelmHooks, "include-helm-hooks", false, "Include helm hooks in the output")
	flag.BoolVar(&config.FailFast, "fail-fast", false, "Exit early if an error occurred")
	flag.IntVar(&config.Workers, "workers", runtime.NumCPU(), "Workers used to parse manifests")
	flag.StringVarP(&config.KubeVersion, "kube-version", "", "", "Kubernetes version (Some helm charts validate manifests against a specific kubernetes version)")
	flag.StringSliceVarP(&config.APIVersions, "api-versions", "", nil, "Kubernetes api versions used for Capabilities.APIVersions (Comma separated)")
	flag.StringVar(&config.Cache, "cache", "inmemory", "Which Helm cache to use, one of none, inmemory, fs")
	flag.StringVar(&config.CacheDir, "cache-dir", getDefaultCacheDir(), "Path to helm chart cache (only used in combination with cache=fs)")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	ctx := context.Background()
	if err := envconfig.Process(ctx, config); err != nil {
		log.Fatal(err)
	}

	flag.Parse()

	if config.Workers < 1 {
		config.Workers = runtime.NumCPU()
	}

	logger, err := buildLogger()
	must(err)

	kubeVersion := &chartutil.KubeVersion{
		Major:   "1",
		Minor:   "31",
		Version: "1.31.0",
	}

	paths := flag.Args()
	if len(paths) == 0 {
		if os.Getenv("PATHS") != "" {
			paths = strings.Split(os.Getenv("PATHS"), ",")
		} else {
			must(errors.New("path to kustomize overlay required"))
		}
	}

	if config.KubeVersion != "" {
		v, err := chartutil.ParseKubeVersion(config.KubeVersion)
		if err != nil {
			must(err)
		}

		kubeVersion = v
	}

	cache, err := cachemgr.New(config.Cache, config.CacheDir)
	if err != nil {
		must(err)
	}

	out, err := os.OpenFile(config.Output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0775)
	must(err)

	a := action.Action{
		AllowFailure:     config.AllowFailure,
		FailFast:         config.FailFast,
		Workers:          config.Workers,
		APIVersions:      config.APIVersions,
		Paths:            paths,
		KubeVersion:      kubeVersion,
		Output:           out,
		IncludeHelmHooks: config.IncludeHelmHooks,
		Logger:           logger,
		Cache:            cache,
	}

	must(a.Run(ctx))
}

func buildLogger() (logr.Logger, error) {
	logOpts := zap.NewDevelopmentConfig()
	logOpts.Encoding = config.Log.Encoding

	err := logOpts.Level.UnmarshalText([]byte(config.Log.Level))
	if err != nil {
		return logr.Discard(), err
	}

	zapLog, err := logOpts.Build()
	if err != nil {
		return logr.Discard(), err
	}

	return zapr.NewLogger(zapLog), nil
}
