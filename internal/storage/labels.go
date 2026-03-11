package storage

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	keyCompositionID = "krateo.io/composition-id"
)

func hasCompositionId(objUn *unstructured.Unstructured) (string, bool) {
	labels := objUn.GetLabels()
	var val string
	var ok bool

	if len(labels) > 0 {
		val, ok = labels[keyCompositionID]
	}

	if !ok && strings.Contains(objUn.GetAPIVersion(), "composition.krateo.io") {
		return string(objUn.GetUID()), true
	}
	return val, ok
}
