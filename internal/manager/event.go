package manager

import (
	"github.com/krateoplatformops/plumbing/eventbus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	CRDCREATE = "crdcreate"
)

type InformerCreateEventCrd struct {
	Name string                    `json:"name"`
	Obj  unstructured.Unstructured `json:"obj"`
}

func (InformerCreateEventCrd) EventID() eventbus.EventID {
	return eventbus.EventID(CRDCREATE)
}
