package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krateoplatformops/plumbing/eventbus"
	"github.com/krateoplatformops/resources-ingester/internal/batch"
	"github.com/krateoplatformops/resources-ingester/internal/objects"
	"github.com/krateoplatformops/resources-ingester/internal/queue"
	"github.com/krateoplatformops/resources-ingester/internal/router"

	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/rest"
)

type StorageOpts struct {
	RESTConfig  *rest.Config
	Eventbus    eventbus.Bus
	Log         *slog.Logger
	Queue       queue.Queuer
	Pool        *pgxpool.Pool
	RecordChan  chan<- batch.InsertRecord
	ClusterName string
}

func NewManager(opts StorageOpts) (*storage, error) {
	objectResolver, err := objects.NewObjectResolver(opts.RESTConfig)
	if err != nil {
		return nil, err
	}
	return &storage{
		eventbus:       opts.Eventbus,
		objectResolver: objectResolver,
		log:            opts.Log,
		clusterName:    opts.ClusterName,
		pool:           opts.Pool,
		queue:          opts.Queue,
		recordChan:     opts.RecordChan,
	}, nil
}

type storage struct {
	eventbus       eventbus.Bus
	objectResolver *objects.ObjectResolver
	log            *slog.Logger
	queue          queue.Queuer
	pool           *pgxpool.Pool
	recordChan     chan<- batch.InsertRecord
	clusterName    string
}

func (ing *storage) Run(stop <-chan struct{}) {
	defer utilruntime.HandleCrash()

	sub := ing.eventbus.Subscribe(router.InformerEvent{}.EventID(), func(ctx context.Context, event eventbus.Event) error {
		eI := event.(router.InformerEvent)
		if eI.EventTarget != router.STORAGE {
			return nil
		}

		switch eI.EventType {
		case router.CREATE, router.UPDATE:
			// Remove the deletion timestamp until the resource is actually deleted
			if eI.Obj.GetDeletionTimestamp() != nil {
				eI.Obj.SetDeletionTimestamp(nil)
			}
		case router.DELETE:
			if eI.Obj.GetDeletionTimestamp() == nil {
				now := v1.Now()
				eI.Obj.SetDeletionTimestamp(&now)
			}
		}
		compositionId, ok := hasCompositionId(eI.Obj)
		if !ok {
			ing.log.Warn("CompositionId not found",
				slog.String("name", eI.Obj.GetName()),
				slog.String("apiversion", eI.Obj.GetAPIVersion()),
				slog.String("kind", eI.Obj.GetKind()),
				slog.String("compositionId", compositionId),
			)
		}

		ing.log.Debug("Storage handling informer event for eI.Object",
			slog.String("name", eI.Obj.GetName()),
			slog.String("apiversion", eI.Obj.GetAPIVersion()),
			slog.String("kind", eI.Obj.GetKind()),
			slog.String("compositionId", compositionId),
		)

		rec := ing.buildRecord(eI.Obj, compositionId, eI.Resource)
		if rec == nil {
			return fmt.Errorf("cannot store: error generating record")
		}
		if rec.UID == "" {
			return fmt.Errorf("cannot store: UID is empty")
		}

		job := &batch.InsertRecordJob{
			Record: *rec,
			Input:  ing.recordChan,
		}

		ing.queue.Push(job)

		return nil
	})
	defer ing.eventbus.Unsubscribe(sub)

	ing.log.Info("Storage started")
	<-stop
	ing.log.Info("Storage stopped")
}

func (ing *storage) buildRecord(obj *unstructured.Unstructured, compositionID string, resource string) *batch.InsertRecord {
	api := obj.GetAPIVersion()
	gv, err := schema.ParseGroupVersion(api)
	if err != nil {
		return nil
	}
	kind := obj.GetKind()

	raw, _ := json.Marshal(obj.Object)
	ca := obj.GetCreationTimestamp()
	ua := v1.Now()
	return &batch.InsertRecord{
		CreatedAt: &ca,
		UpdatedAt: &ua,
		DeletedAt: obj.GetDeletionTimestamp(),

		ClusterName: ing.clusterName,
		UID:         string(obj.GetUID()),
		GlobalUID:   fmt.Sprintf("%s:%s", ing.clusterName, string(obj.GetUID())),

		Namespace:       obj.GetNamespace(),
		ResourceName:    obj.GetName(),
		ResourceGroup:   gv.Group,
		ResourceVersion: gv.Version,
		ResourceKind:    kind,
		ResourcePlural:  resource,

		CompositionID: compositionID,

		Raw: raw,
	}
}
