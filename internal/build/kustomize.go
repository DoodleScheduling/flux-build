package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/provider"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/resource"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

var kustomizeBuildMutex sync.Mutex

func Kustomize(ctx context.Context, path string) (resmap.ResMap, error) {
	kfile := filepath.Join(path, konfig.DefaultKustomizationFileName())
	fs := filesys.MakeFsOnDisk()
	pvd := provider.NewDefaultDepProvider()
	singleFile := false

	_, err := os.Stat(kfile)
	if err != nil {
		stat, err := os.Stat(path)
		if err != nil {
			return nil, err
		}

		if path == "/dev/stdin" || path == "-" {
			singleFile = true
			d, err := os.MkdirTemp(os.TempDir(), "")
			if err != nil {
				return nil, err
			}

			f, err := os.OpenFile(filepath.Join(d, "stdin.yaml"), os.O_CREATE|os.O_RDWR, 0644)
			if err != nil {
				return nil, err
			}

			_, err = io.Copy(f, os.Stdin)
			if err != nil {
				return nil, err
			}

			path = d

			defer func() {
				_ = os.RemoveAll(d)
			}()
		} else if !stat.IsDir() {
			singleFile = true
			d, err := os.MkdirTemp(os.TempDir(), "")
			if err != nil {
				return nil, err
			}

			fullPath, err := filepath.Abs(path)
			if err != nil {
				return nil, err
			}

			if err := os.Symlink(fullPath, filepath.Join(d, filepath.Base(path))); err != nil {
				return nil, err
			}

			path = d

			defer func() {
				_ = os.RemoveAll(d)
			}()
		}

		defer func() {
			_ = os.Remove(kfile)
		}()

		err = createKustomization(path, fs, pvd.GetResourceFactory(), singleFile)
		if err != nil {
			return nil, fmt.Errorf("failed create kustomization: %w", err)
		}
	}

	buildOptions := &krusty.Options{
		LoadRestrictions:  kustypes.LoadRestrictionsNone,
		AddManagedbyLabel: false,
		PluginConfig:      krusty.MakeDefaultOptions().PluginConfig,
	}

	kustomizeBuildMutex.Lock()
	defer kustomizeBuildMutex.Unlock()

	kustomizer := krusty.MakeKustomizer(buildOptions)
	return kustomizer.Run(fs, path)
}

func createKustomization(path string, fSys filesys.FileSystem, rf *resource.Factory, singleFile bool) error {
	kfile := filepath.Join(path, konfig.DefaultKustomizationFileName())
	kus := kustypes.Kustomization{
		TypeMeta: kustypes.TypeMeta{
			APIVersion: kustypes.KustomizationVersion,
			Kind:       kustypes.KustomizationKind,
		},
	}

	detected, err := detectResources(fSys, rf, path, singleFile)
	if err != nil {
		return err
	}

	kus.Resources = append(kus.Resources, detected...)

	kd, err := yaml.Marshal(kus)
	if err != nil {
		return err
	}

	return os.WriteFile(kfile, kd, os.ModePerm)
}

func detectResources(fSys filesys.FileSystem, rf *resource.Factory, base string, singleFile bool) ([]string, error) {
	var paths []string

	err := fSys.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		normalizedPath, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}

		if path == base {
			return nil
		}

		if info.IsDir() {
			// If a sub-directory contains an existing kustomization file add the
			// directory as a resource and do not decend into it.
			for _, kfilename := range konfig.RecognizedKustomizationFileNames() {
				if fSys.Exists(filepath.Join(path, kfilename)) {
					paths = append(paths, normalizedPath)
					return filepath.SkipDir
				}
			}
			return nil
		}
		fContents, err := fSys.ReadFile(path)
		if err != nil {
			return err
		}
		if _, err := rf.SliceFromBytes(fContents); err != nil {
			if singleFile {
				return err
			}

			return nil
		}

		paths = append(paths, normalizedPath)
		return nil
	})

	return paths, err
}
