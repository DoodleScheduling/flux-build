package build

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	sourcev1beta2 "github.com/fluxcd/source-controller/api/v1beta2"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/kustomize/api/resource"

	"github.com/doodlescheduling/flux-build/internal/helm/chart"
	chartcache "github.com/doodlescheduling/flux-build/internal/helm/chart/cache"
)

func TestHelm_getGitRepositorySecret(t *testing.T) {
	tests := []struct {
		name       string
		repository *sourcev1beta2.GitRepository
		db         map[ref]*resource.Resource
		want       *corev1.Secret
		wantErr    string
	}{
		{
			name: "repository without secret ref",
			repository: &sourcev1beta2.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "default",
				},
				Spec: sourcev1beta2.GitRepositorySpec{
					URL: "https://github.com/example/repo",
				},
			},
			db:   map[ref]*resource.Resource{},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			h := NewHelmBuilder(logr.Discard(), HelmOpts{})

			got, err := h.getGitRepositorySecret(tt.repository, tt.db)

			if tt.wantErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.wantErr))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			if tt.want == nil {
				g.Expect(got).To(BeNil())
			} else {
				g.Expect(got).ToNot(BeNil())
				g.Expect(got.Name).To(Equal(tt.want.Name))
				g.Expect(got.Namespace).To(Equal(tt.want.Namespace))
			}
		})
	}
}

func TestHelm_configureGitAuth(t *testing.T) {
	tests := []struct {
		name    string
		secret  *corev1.Secret
		want    *http.BasicAuth
		wantErr string
	}{
		{
			name:   "nil secret",
			secret: nil,
			want:   nil,
		},
		{
			name: "valid secret with username and password",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte("testpass"),
				},
			},
			want: &http.BasicAuth{
				Username: "testuser",
				Password: "testpass",
			},
		},
		{
			name: "secret missing username",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"password": []byte("testpass"),
				},
			},
			wantErr: "secret must contain both 'username' and 'password' keys",
		},
		{
			name: "secret missing password",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"username": []byte("testuser"),
				},
			},
			wantErr: "secret must contain both 'username' and 'password' keys",
		},
		{
			name: "secret with empty username",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"username": []byte(""),
					"password": []byte("testpass"),
				},
			},
			wantErr: "username and password cannot be empty",
		},
		{
			name: "secret with empty password",
			secret: &corev1.Secret{
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte(""),
				},
			},
			wantErr: "username and password cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			h := NewHelmBuilder(logr.Discard(), HelmOpts{})

			got, err := h.configureGitAuth(tt.secret)

			if tt.wantErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.wantErr))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			if tt.want == nil {
				g.Expect(got).To(BeNil())
			} else {
				g.Expect(got).ToNot(BeNil())
				g.Expect(got.Username).To(Equal(tt.want.Username))
				g.Expect(got.Password).To(Equal(tt.want.Password))
			}
		})
	}
}

func TestHelm_cloneAndExtractChart(t *testing.T) {
	// Create a temporary git repository for testing
	tempRepoDir := createTempGitRepo(t)
	defer os.RemoveAll(tempRepoDir)

	tests := []struct {
		name      string
		repoURL   string
		gitRef    *sourcev1beta2.GitRepositoryRef
		chartPath string
		auth      *http.BasicAuth
		wantErr   string
	}{
		{
			name:      "clone repository with default branch",
			repoURL:   tempRepoDir,
			gitRef:    nil, // Should default to master
			chartPath: "chart",
			auth:      nil,
		},
		{
			name:    "clone repository with specific branch",
			repoURL: tempRepoDir,
			gitRef: &sourcev1beta2.GitRepositoryRef{
				Branch: "master",
			},
			chartPath: "chart",
			auth:      nil,
		},
		{
			name:      "chart path not found",
			repoURL:   tempRepoDir,
			gitRef:    nil,
			chartPath: "nonexistent",
			auth:      nil,
			wantErr:   "chart path nonexistent not found in repository",
		},
		{
			name:      "invalid repository URL",
			repoURL:   "https://invalid-repo-url-that-does-not-exist.com/repo.git",
			gitRef:    nil,
			chartPath: "chart",
			auth:      nil,
			wantErr:   "failed to clone git repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			h := NewHelmBuilder(logr.Discard(), HelmOpts{})

			tempDir, err := os.MkdirTemp("", "helm-test-")
			g.Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(tempDir)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			got, err := h.cloneAndExtractChart(ctx, tt.repoURL, tt.gitRef, tt.chartPath, tempDir, tt.auth)

			if tt.wantErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.wantErr))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).ToNot(BeEmpty())

			// Verify the path exists and contains the expected chart directory
			_, err = os.Stat(got)
			g.Expect(err).ToNot(HaveOccurred())
		})
	}
}

func TestHelm_buildFromGitRepository(t *testing.T) {
	// Create a temporary git repository with a valid Helm chart
	tempRepoDir := createTempGitRepoWithChart(t)
	defer os.RemoveAll(tempRepoDir)

	tests := []struct {
		name    string
		chart   *sourcev1beta2.HelmChart
		repo    *sourcev1beta2.GitRepository
		db      map[ref]*resource.Resource
		wantErr string
	}{
		{
			name: "successful build from git repository",
			chart: &sourcev1beta2.HelmChart{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-chart",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: sourcev1beta2.HelmChartSpec{
					Chart:   "testchart",
					Version: "*",
				},
			},
			repo: &sourcev1beta2.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "default",
				},
				Spec: sourcev1beta2.GitRepositorySpec{
					URL: tempRepoDir,
				},
			},
			db: map[ref]*resource.Resource{},
		},
		{
			name: "invalid chart path",
			chart: &sourcev1beta2.HelmChart{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-chart",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: sourcev1beta2.HelmChartSpec{
					Chart:   "nonexistent",
					Version: "*",
				},
			},
			repo: &sourcev1beta2.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-repo",
					Namespace: "default",
				},
				Spec: sourcev1beta2.GitRepositorySpec{
					URL: tempRepoDir,
				},
			},
			db:      map[ref]*resource.Resource{},
			wantErr: "chart path nonexistent not found in repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create a temporary directory for cache
			cacheDir, err := os.MkdirTemp("", "chart-cache-")
			g.Expect(err).ToNot(HaveOccurred())
			defer os.RemoveAll(cacheDir)

			// Create cache for testing
			cache, err := chartcache.New("fs", cacheDir)
			g.Expect(err).ToNot(HaveOccurred())

			h := NewHelmBuilder(logr.Discard(), HelmOpts{
				Cache: cache,
			})

			b := &chart.Build{}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			err = h.buildFromGitRepository(ctx, tt.chart, tt.repo, b, tt.db)

			if tt.wantErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.wantErr))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(b).ToNot(BeNil())
			g.Expect(b.Path).ToNot(BeEmpty())

			// Verify the chart was built successfully
			_, err = os.Stat(b.Path)
			g.Expect(err).ToNot(HaveOccurred())
		})
	}
}

// Helper functions for creating test data

func createTempGitRepo(t *testing.T) string {
	tempDir, err := os.MkdirTemp("", "git-repo-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Initialize git repository
	repo, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Create a chart directory
	chartDir := filepath.Join(tempDir, "chart")
	err = os.MkdirAll(chartDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create chart dir: %v", err)
	}

	// Create a basic file in the chart directory
	err = os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte("name: test-chart\nversion: 1.0.0\n"), 0644)
	if err != nil {
		t.Fatalf("Failed to create Chart.yaml: %v", err)
	}

	// Add and commit files
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Failed to get worktree: %v", err)
	}

	_, err = worktree.Add(".")
	if err != nil {
		t.Fatalf("Failed to add files: %v", err)
	}

	_, err = worktree.Commit("Initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	return tempDir
}

func createTempGitRepoWithChart(t *testing.T) string {
	tempDir, err := os.MkdirTemp("", "git-repo-chart-test-")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Initialize git repository
	repo, err := git.PlainInit(tempDir, false)
	if err != nil {
		t.Fatalf("Failed to init git repo: %v", err)
	}

	// Create a valid Helm chart structure
	chartDir := filepath.Join(tempDir, "testchart")
	err = os.MkdirAll(chartDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create chart dir: %v", err)
	}

	templatesDir := filepath.Join(chartDir, "templates")
	err = os.MkdirAll(templatesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create templates dir: %v", err)
	}

	// Create Chart.yaml
	chartYaml := `apiVersion: v2
name: testchart
description: A test Helm chart
version: 0.1.0
appVersion: 1.0.0
`
	err = os.WriteFile(filepath.Join(chartDir, "Chart.yaml"), []byte(chartYaml), 0644)
	if err != nil {
		t.Fatalf("Failed to create Chart.yaml: %v", err)
	}

	// Create values.yaml
	valuesYaml := `replicaCount: 1
image:
  repository: nginx
  tag: latest
`
	err = os.WriteFile(filepath.Join(chartDir, "values.yaml"), []byte(valuesYaml), 0644)
	if err != nil {
		t.Fatalf("Failed to create values.yaml: %v", err)
	}

	// Create a simple template
	deploymentTemplate := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "testchart.fullname" . }}
spec:
  replicas: {{ .Values.replicaCount }}
  template:
    spec:
      containers:
      - name: {{ .Chart.Name }}
        image: "{{ .Values.image.repository }}:{{ .Values.image.tag }}"
`
	err = os.WriteFile(filepath.Join(templatesDir, "deployment.yaml"), []byte(deploymentTemplate), 0644)
	if err != nil {
		t.Fatalf("Failed to create deployment template: %v", err)
	}

	// Add and commit files
	worktree, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Failed to get worktree: %v", err)
	}

	_, err = worktree.Add(".")
	if err != nil {
		t.Fatalf("Failed to add files: %v", err)
	}

	_, err = worktree.Commit("Add test chart", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("Failed to commit: %v", err)
	}

	return tempDir
}
