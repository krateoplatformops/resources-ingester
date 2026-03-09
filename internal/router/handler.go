package router

import (
	"context"
	"log/slog"

	"github.com/krateoplatformops/plumbing/eventbus"
	"github.com/krateoplatformops/resources-ingester/internal/batch"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type IngesterOpts struct {
	Log      *slog.Logger
	EventBus eventbus.Bus
}

func NewIngester(opts IngesterOpts) (EventHandler, error) {
	return &ingester{
		log:      opts.Log,
		eventbus: opts.EventBus,
	}, nil
}

var _ EventHandler = (*ingester)(nil)

type ingester struct {
	log         *slog.Logger
	recordChan  chan<- batch.InsertRecord
	clusterName string
	eventbus    eventbus.Bus
}

func (ing *ingester) Handle(obj *unstructured.Unstructured, op Operation, tg Target) {
	ing.eventbus.PublishAsync(context.Background(), InformerEvent{
		Name:        obj.GetName(),
		EventType:   op,
		EventTarget: tg,
		Obj:         *obj,
	})

}
