package postrenderer

import (
	"bytes"

	v2 "github.com/fluxcd/helm-controller/api/v2beta1"
	"sigs.k8s.io/kustomize/api/provider"
	"sigs.k8s.io/kustomize/api/resmap"
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
	resFactory := provider.NewDefaultDepProvider().GetResourceFactory()
	resMapFactory := resmap.NewFactory(resFactory)

	resMap, err := resMapFactory.NewResMapFromBytes(renderedManifests.Bytes())
	if err != nil {
		return nil, err
	}

	for _, resource := range resMap.Resources() {
		if resource.GetNamespace() == "" {
			err = resource.SetNamespace(k.namespace)
			if err != nil {
				return nil, err
			}
		}
	}

	yaml, err := resMap.AsYaml()
	if err != nil {
		return nil, err
	}

	return bytes.NewBuffer(yaml), nil
}
