package router

import (
	"github.com/krateoplatformops/plumbing/eventbus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	CRDCREATE = "informerevent"
)

type InformerEvent struct {
	Name      string                    `json:"name"`
	EventType Operation                 `json:"eventType"`
	Obj       unstructured.Unstructured `json:"obj"`
}

func (InformerEvent) EventID() eventbus.EventID {
	return eventbus.EventID(CRDCREATE)
}
