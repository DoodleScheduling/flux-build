package main

import (
	"context"
	"os"

	"github.com/doodlescheduling/flux-kustomize-action/internal/action"
	"github.com/sethvargo/go-githubactions"
)

func run() error {
	ctx := context.Background()
	ghaction := githubactions.New()

	a, err := action.NewFromInputs(ctx, ghaction)
	if err != nil {
		return err
	}

	return a.Run(ctx)
}

func main() {
	workDir := os.Getenv("GITHUB_WORKSPACE")
	if workDir != "" {
		_ = os.Chdir(workDir)
	}

	err := run()
	if err != nil {
		githubactions.Fatalf("%v", err)
	}
}
