package build

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/kustomize/api/konfig"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/provider"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/api/resource"
	kustypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/yaml"
)

type KustomizeOpts struct {
	Path string
}

type Kustomize struct {
	*os.File
	opts      KustomizeOpts
	resources map[ref]*resource.Resource
}

func NewKustomizeBuilder(opts KustomizeOpts) *Kustomize {
	return &Kustomize{
		opts:      opts,
		resources: make(map[ref]*resource.Resource),
	}
}

func (k *Kustomize) Build(ctx context.Context) error {
	resourcesMap, err := k.buildKustomization(k.opts.Path)
	if err != nil {
		return fmt.Errorf("failed build kustomization: %w", err)
	}

	// create index by resource
	for _, r := range resourcesMap.Resources() {
		resMeta, err := r.RNode.GetMeta()
		if err != nil {
			return err
		}

		gvk := schema.FromAPIVersionAndKind(resMeta.APIVersion, resMeta.Kind)

		k.resources[ref{
			GroupKind: schema.GroupKind{
				Group: gvk.Group,
				Kind:  gvk.Kind,
			},
			Name:      resMeta.Name,
			Namespace: resMeta.Namespace,
		}] = r
	}

	kustomizeBuild, err := resourcesMap.AsYaml()
	if err != nil {
		return fmt.Errorf("failed marshal resources as yaml: %w", err)
	}

	out, err := os.CreateTemp("", "yaml")
	if err != nil {
		return fmt.Errorf("failed create output manifest file: %w", err)
	}

	_, err = out.Write(kustomizeBuild)
	if err != nil {
		return fmt.Errorf("failed to write kustomize build manifests to output: %w", err)
	}

	k.File = out

	return nil
}

func (k *Kustomize) Resources() map[ref]*resource.Resource {
	return k.resources
}

func (k *Kustomize) buildKustomization(path string) (resmap.ResMap, error) {
	kfile := filepath.Join(path, konfig.DefaultKustomizationFileName())
	fs := filesys.MakeFsOnDisk()

	_, err := os.Stat(kfile)
	if err != nil {
		curPath, err := os.Getwd()
		if err != nil {
			return nil, err
		}

		if err := os.Chdir(path); err != nil {
			return nil, err
		}

		defer func() {
			_ = os.Remove(kfile)
			_ = os.Chdir(curPath)
		}()

		pvd := provider.NewDefaultDepProvider()
		err = k.createKustomization(".", fs, pvd.GetResourceFactory())
		if err != nil {
			return nil, err
		}
	}

	buildOptions := &krusty.Options{
		LoadRestrictions:  kustypes.LoadRestrictionsNone,
		AddManagedbyLabel: false,
		PluginConfig:      krusty.MakeDefaultOptions().PluginConfig,
	}

	kustomizer := krusty.MakeKustomizer(buildOptions)
	return kustomizer.Run(fs, path)
}

func (k *Kustomize) createKustomization(path string, fSys filesys.FileSystem, rf *resource.Factory) error {
	kfile := filepath.Join(path, konfig.DefaultKustomizationFileName())
	kus := kustypes.Kustomization{
		TypeMeta: kustypes.TypeMeta{
			APIVersion: kustypes.KustomizationVersion,
			Kind:       kustypes.KustomizationKind,
		},
	}

	detected, err := k.detectResources(fSys, rf, path, true)
	if err != nil {
		return err
	}

	kus.Resources = append(kus.Resources, detected...)

	kd, err := yaml.Marshal(kus)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(kfile, kd, os.ModePerm)
}

func (k *Kustomize) detectResources(fSys filesys.FileSystem, rf *resource.Factory, base string, recursive bool) ([]string, error) {
	var paths []string
	err := fSys.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == base {
			return nil
		}
		if info.IsDir() {
			if !recursive {
				return filepath.SkipDir
			}
			// If a sub-directory contains an existing kustomization file add the
			// directory as a resource and do not decend into it.
			for _, kfilename := range konfig.RecognizedKustomizationFileNames() {
				if fSys.Exists(filepath.Join(path, kfilename)) {
					paths = append(paths, path)
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
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}

type ref struct {
	schema.GroupKind
	Name      string
	Namespace string
}
