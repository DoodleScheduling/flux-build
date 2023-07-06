package main

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"

	"github.com/doodlescheduling/flux-build/internal/action"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	flag "github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"helm.sh/helm/v3/pkg/chartutil"
)

var (
	log          logr.Logger
	allowFailure bool
	failFast     bool
	workers      int = runtime.NumCPU()
	output       string
	apiVersions  string
	cacheDir     string
	kubeVersion  string
)

func must(err error) {
	if err != nil {
		log.Error(err, "error encounterd")
		os.Exit(1)
	}
}

func main() {
	zapLog, err := zap.NewDevelopment()
	if err != nil {
		panic(err)
	}

	log = zapr.NewLogger(zapLog)
	flag.BoolVar(&allowFailure, "allow-failure", allowFailure, "Do not exit > 0 if an error occured")
	flag.BoolVar(&failFast, "fail-fast", failFast, "Exit early if an error occured")
	flag.IntVar(&workers, "workers", workers, "Workers used to template the HelmReleases. Greatly improves speed if there are many HelmReleases")
	flag.StringVar(&cacheDir, "cache-dir", cacheDir, "Cache directory (for repositorieries, charts)")
	flag.StringVar(&kubeVersion, "kube-version", kubeVersion, "Kubernetes version (Some helm charts validate manifests against a specific kubernetes version)")
	flag.StringVar(&apiVersions, "api-versions", apiVersions, "Kubernetes api versions used for Capabilities.APIVersions (Comma separated)")
	flag.StringVar(&output, "output", output, "Path to output file")
	flag.Parse()

	// Import flags into viper and bind them to env vars
	// flags are converted to upper-case, - is replaced with _
	err = viper.BindPFlags(flag.CommandLine)
	must(err)

	replacer := strings.NewReplacer("-", "_")
	viper.SetEnvKeyReplacer(replacer)
	viper.AutomaticEnv()

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

	if viper.GetString("kube-version") != "" {
		v, err := chartutil.ParseKubeVersion(viper.GetString("kube-version"))
		if err != nil {
			must(err)
		}

		kubeVersion = v
	}

	output := os.Stdout
	if viper.GetString("output") != "" {
		f, err := os.OpenFile(viper.GetString("output"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		must(err)
		output = f
	}

	a := action.Action{
		AllowFailure: viper.GetBool("allow-failure"),
		FailFast:     viper.GetBool("fail-fast"),
		Workers:      viper.GetInt("workers"),
		CacheDir:     viper.GetString("cache-dir"),
		APIVersions:  strings.Split(viper.GetString("api-versions"), ","),
		Paths:        paths,
		KubeVersion:  kubeVersion,
		Output:       output,
		Logger:       log,
	}

	must(a.Run(context.TODO()))
}
