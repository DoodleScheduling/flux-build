package action

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chartcache "github.com/doodlescheduling/flux-build/internal/helm/chart/cache"
	"github.com/go-logr/logr"
	"helm.sh/helm/v3/pkg/chartutil"
)

// manifests are intentionally supplied out of sorted order and spread across
// separate kustomize paths so that the concurrent worker pools would, without
// the deterministic sort in Run, emit them in a different order on every run.
var testManifests = []struct {
	file    string
	content string
}{
	{"svc.yaml", "apiVersion: v1\nkind: Service\nmetadata:\n  name: svc\n  namespace: test\nspec:\n  ports:\n  - port: 80\n"},
	{"deploy.yaml", "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: deploy\n  namespace: test\nspec:\n  selector:\n    matchLabels:\n      app: x\n  template:\n    metadata:\n      labels:\n        app: x\n    spec:\n      containers:\n      - name: c\n        image: nginx\n"},
	{"cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm\n  namespace: test\ndata:\n  a: b\n"},
	{"ns.yaml", "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: ns\n"},
}

func buildPaths(t *testing.T) []string {
	t.Helper()

	var paths []string
	for _, m := range testManifests {
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, m.file), []byte(m.content), 0o644); err != nil {
			t.Fatalf("write manifest: %v", err)
		}
		paths = append(paths, d)
	}

	return paths
}

func runAction(t *testing.T, paths []string) string {
	t.Helper()

	cache, err := chartcache.New("none", "")
	if err != nil {
		t.Fatalf("cache: %v", err)
	}

	var buf bytes.Buffer
	a := Action{
		Workers:     4,
		Paths:       paths,
		KubeVersion: &chartutil.KubeVersion{Major: "1", Minor: "31", Version: "1.31.0"},
		Output:      &buf,
		Logger:      logr.Discard(),
		Cache:       cache,
	}

	if err := a.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	return buf.String()
}

func kindOrder(out string) []string {
	var kinds []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "kind:") {
			kinds = append(kinds, strings.TrimSpace(strings.TrimPrefix(line, "kind:")))
		}
	}
	return kinds
}

// TestRunDeterministicOrder verifies the rendered output is byte-identical
// across repeated runs and sorted by group/version/kind/namespace/name.
func TestRunDeterministicOrder(t *testing.T) {
	paths := buildPaths(t)

	first := runAction(t, paths)

	for i := 0; i < 10; i++ {
		if got := runAction(t, paths); got != first {
			t.Fatalf("output not deterministic on run %d:\n--- first ---\n%s\n--- got ---\n%s", i, first, got)
		}
	}

	// Core group ("") sorts before "apps"; within core, kinds sort
	// alphabetically. Deployment (apps/v1) therefore comes last.
	want := []string{"ConfigMap", "Namespace", "Service", "Deployment"}
	got := kindOrder(first)

	if len(got) != len(want) {
		t.Fatalf("expected %d resources, got %d (%v)", len(want), len(got), got)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resources not in deterministic sorted order:\nwant %v\ngot  %v", want, got)
		}
	}
}
