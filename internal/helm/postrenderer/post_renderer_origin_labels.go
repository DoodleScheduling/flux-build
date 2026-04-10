/*
Copyright 2021 The Flux authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package postrenderer

import (
	"bytes"
	"fmt"

	helmv2 "github.com/fluxcd/helm-controller/api/v2"
	"sigs.k8s.io/kustomize/api/provider"
	"sigs.k8s.io/kustomize/api/resmap"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

func NewPostRendererOriginLabels(release *helmv2.HelmRelease) *postRendererOriginLabels {
	return &postRendererOriginLabels{
		name:      release.Name,
		namespace: release.Namespace,
	}
}

type postRendererOriginLabels struct {
	name      string
	namespace string
}

func (k *postRendererOriginLabels) Run(renderedManifests *bytes.Buffer) (modifiedManifests *bytes.Buffer, err error) {
	resFactory := provider.NewDefaultDepProvider().GetResourceFactory()
	resMapFactory := resmap.NewFactory(resFactory)

	resMap, err := resMapFactory.NewResMapFromBytes(renderedManifests.Bytes())
	if err != nil {
		return nil, err
	}

	labels := originLabels(k.name, k.namespace)
	for _, res := range resMap.Resources() {
		for key, val := range labels {
			if err := res.PipeE(yaml.SetLabel(key, val)); err != nil {
				return nil, err
			}
		}
	}

	yamlOut, err := resMap.AsYaml()
	if err != nil {
		return nil, err
	}

	return bytes.NewBuffer(yamlOut), nil
}

func originLabels(name, namespace string) map[string]string {
	return map[string]string{
		fmt.Sprintf("%s/name", helmv2.GroupVersion.Group):      name,
		fmt.Sprintf("%s/namespace", helmv2.GroupVersion.Group): namespace,
	}
}
