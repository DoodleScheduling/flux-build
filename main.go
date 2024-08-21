package main

import (
	"context"
	"errors"
	"log"
	"os"
	"runtime"
	"strings"

	"github.com/doodlescheduling/flux-build/internal/action"
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
}

var (
	config = &Config{}
)

func init() {
	flag.StringVarP(&config.Log.Level, "log-level", "l", "", "Define the log level (default is warning) [debug,info,warn,error]")
	flag.StringVarP(&config.Log.Encoding, "log-encoding", "e", "", "Define the log format (default is json) [json,console]")
	flag.StringVarP(&config.Output, "output", "o", "", "Path to output")
	flag.BoolVar(&config.AllowFailure, "allow-failure", false, "Do not exit > 0 if an error occured")
	flag.BoolVar(&config.IncludeHelmHooks, "include-helm-hooks", false, "Include helm hooks in the output")
	flag.BoolVar(&config.FailFast, "fail-fast", false, "Exit early if an error occured")
	flag.IntVar(&config.Workers, "workers", 0, "Workers used to parse manifests")
	flag.StringVarP(&config.KubeVersion, "kube-version", "", "", "Kubernetes version (Some helm charts validate manifests against a specific kubernetes version)")
	flag.StringSliceVarP(&config.APIVersions, "api-versions", "", nil, "Kubernetes api versions used for Capabilities.APIVersions (Comma separated)")
	flag.BoolVar(&config.CacheEnabled, "cache-enabled", true, "Is Helm charts cache enabled")
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

	if config.Workers == 0 {
		config.Workers = runtime.NumCPU()
	}

	logger, err := buildLogger()
	must(err)

	kubeVersion := &chartutil.KubeVersion{
		Major:   "1",
		Minor:   "27",
		Version: "1.27.0",
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
		CacheEnabled:     config.CacheEnabled,
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
