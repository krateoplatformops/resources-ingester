package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krateoplatformops/plumbing/eventbus"
	"github.com/krateoplatformops/resources-ingester/internal/batch"
	"github.com/krateoplatformops/resources-ingester/internal/objects"
	"github.com/krateoplatformops/resources-ingester/internal/queue"
	"github.com/krateoplatformops/resources-ingester/internal/router"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

		ref := &corev1.ObjectReference{
			Kind:            eI.Obj.GetKind(),
			APIVersion:      eI.Obj.GetAPIVersion(),
			Name:            eI.Obj.GetName(),
			Namespace:       eI.Obj.GetNamespace(),
			UID:             eI.Obj.GetUID(),
			ResourceVersion: eI.Obj.GetResourceVersion(),
		}
		compositionId, ok := hasCompositionId(&eI.Obj)
		if !ok {
			var err error
			compositionId, err = findCompositionID(ing.objectResolver, ref, ing.log)
			if err != nil {
				if !errors.Is(err, ErrCompositionIdNotFound) {
					ing.log.Error("unable to look for composition id",
						slog.String("eI.Obj", ref.Name),
						slog.Any("err", err),
					)
					return err
				}
			}
		}

		ing.log.Debug("Storage handling informer event for eI.Object",
			slog.String("name", eI.Obj.GetName()),
			slog.String("apiversion", eI.Obj.GetAPIVersion()),
			slog.String("kind", eI.Obj.GetKind()),
			slog.String("compositionId", compositionId),
		)

		rec := ing.buildRecord(eI.Obj, compositionId)
		if rec.UID == "" {
			return fmt.Errorf("cannot store: UID is empty")
		}

		job := &batch.InsertRecordJob{
			Record: rec,
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

func (ing *storage) buildRecord(obj unstructured.Unstructured, compositionID string) batch.InsertRecord {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[keyCompositionID] = compositionID

	api := obj.GetAPIVersion()
	kind := obj.GetKind()
	resourceKind := kind
	if api != "" {
		resourceKind = api + "." + kind
	}

	cid := pgtype.UUID{Valid: false}
	if compositionID != "" {
		uid, err := uuid.Parse(compositionID)
		if err == nil {
			cid = pgtype.UUID{
				Bytes: uid,
				Valid: true,
			}
		}
	}

	raw, _ := json.Marshal(obj.Object)

	return batch.InsertRecord{
		ClusterName:     ing.clusterName,
		UID:             string(obj.GetUID()),
		GlobalUID:       fmt.Sprintf("%s:%s", ing.clusterName, string(obj.GetUID())),
		Namespace:       obj.GetNamespace(),
		ResourceKind:    resourceKind,
		ResourceName:    obj.GetName(),
		CompositionID:   cid,
		ResourceVersion: obj.GetResourceVersion(),
		Raw:             raw,
	}
}
