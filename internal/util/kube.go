package util

import (
	"github.com/krateoplatformops/plumbing/kubeutil/plurals"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func GetGVR(gvk schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	gvr, err := plurals.Get(gvk, plurals.GetOptions{})
	if err != nil {
		return schema.GroupVersionResource{}, nil
	}
	return schema.GroupVersionResource{
		Group:    gvk.Group,
		Version:  gvk.Version,
		Resource: gvr.Plural,
	}, nil
}

// This function returns the GVR for the CRs to watch for a given CRD and also tells whether its cluster or namespaced-scoped
// Includes all versions
// Static
func ExtractGvrNsFromCrd(crd *unstructured.Unstructured) ([]schema.GroupVersionResource, bool) {
	res := []schema.GroupVersionResource{}

	spec, ok := crd.Object["spec"].(map[string]interface{})
	if !ok {
		return res, false
	}

	scope, _ := spec["scope"].(string)
	namespaced := scope == "Namespaced"

	group, _ := spec["group"].(string)

	names, ok := spec["names"].(map[string]interface{})
	if !ok {
		return res, namespaced
	}
	kind, _ := names["kind"].(string)

	versions, ok := spec["versions"].([]interface{})
	if !ok {
		return res, namespaced
	}

	for _, v := range versions {
		submap, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		version, _ := submap["name"].(string)
		if version == "" {
			continue
		}

		gv := schema.GroupVersionKind{
			Group:   group,
			Version: version,
			Kind:    kind,
		}
		gvr, err := GetGVR(gv)
		if err != nil {
			continue
		}
		res = append(res, gvr)
	}

	return res, namespaced
}
