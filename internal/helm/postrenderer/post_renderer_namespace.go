package postrenderer

import (
	"bytes"
	"encoding/json"

	v2 "github.com/fluxcd/helm-controller/api/v2beta1"
	"sigs.k8s.io/kustomize/api/filesys"
	kustypes "sigs.k8s.io/kustomize/api/types"
)

func NewPostRendererNamespace(release *v2.HelmRelease) *postRendererNamespace {
	ns := release.GetReleaseNamespace()
	if ns == "" {
		ns = "default"
	}

	return &postRendererNamespace{
		namespace: ns,
	}
}

type postRendererNamespace struct {
	namespace string
}

func (k *postRendererNamespace) Run(renderedManifests *bytes.Buffer) (modifiedManifests *bytes.Buffer, err error) {
	fs := filesys.MakeFsInMemory()
	cfg := kustypes.Kustomization{}
	cfg.APIVersion = kustypes.KustomizationVersion
	cfg.Kind = kustypes.KustomizationKind
	cfg.Namespace = k.namespace

	// Add rendered Helm output as input resource to the Kustomization.
	const input = "helm-output.yaml"
	cfg.Resources = append(cfg.Resources, input)
	if err := writeFile(fs, input, renderedManifests); err != nil {
		return nil, err
	}

	// Write kustomization config to file.
	kustomization, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := writeToFile(fs, "kustomization.yaml", kustomization); err != nil {
		return nil, err
	}
	resMap, err := buildKustomization(fs, ".")
	if err != nil {
		return nil, err
	}
	yaml, err := resMap.AsYaml()
	if err != nil {
		return nil, err
	}
	return bytes.NewBuffer(yaml), nil

}
