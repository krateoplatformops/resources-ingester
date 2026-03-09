package router

import (
	"github.com/krateoplatformops/plumbing/eventbus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type Operation string
type Target string

const (
	EVENT = "informerevent"

	CREATE Operation = "create"
	UPDATE Operation = "update"
	DELETE Operation = "delete"

	MANAGER Target = "manager"
	STORAGE Target = "storage"
)

type InformerEvent struct {
	Name        string                    `json:"name"`
	EventType   Operation                 `json:"eventType"`
	EventTarget Target                    `json:"eventTarget"`
	Obj         unstructured.Unstructured `json:"obj"`
}

func (InformerEvent) EventID() eventbus.EventID {
	return eventbus.EventID(EVENT)
}
