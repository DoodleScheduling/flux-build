package build

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/kustomize/api/resource"
)

type ResourceIndex map[ref]*resource.Resource

func (r ResourceIndex) Push(resources []*resource.Resource) error {
	for _, resource := range resources {
		resMeta, err := resource.RNode.GetMeta()
		if err != nil {
			return err
		}

		gvk := schema.FromAPIVersionAndKind(resMeta.APIVersion, resMeta.Kind)

		r[ref{
			GroupKind: schema.GroupKind{
				Group: gvk.Group,
				Kind:  gvk.Kind,
			},
			Name:      resMeta.Name,
			Namespace: resMeta.Namespace,
		}] = resource
	}

	return nil
}
