package router

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krateoplatformops/plumbing/eventbus"
	"github.com/krateoplatformops/resources-ingester/internal/batch"
	"github.com/krateoplatformops/resources-ingester/internal/objects"
	"github.com/krateoplatformops/resources-ingester/internal/queue"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
)

type IngesterOpts struct {
	RESTConfig  *rest.Config
	Queue       queue.Queuer
	Pool        *pgxpool.Pool
	Log         *slog.Logger
	RecordChan  chan<- batch.InsertRecord // NEW
	ClusterName string                    // opzionale ma utile
	EventBus    eventbus.Bus
}

func NewIngester(opts IngesterOpts) (EventHandler, error) {
	objectResolver, err := objects.NewObjectResolver(opts.RESTConfig)
	if err != nil {
		return nil, err
	}

	return &ingester{
		objectResolver: objectResolver,
		notifyQueue:    opts.Queue,
		log:            opts.Log,
		recordChan:     opts.RecordChan,
		clusterName:    opts.ClusterName,
		eventbus:       opts.EventBus,
	}, nil
}

var _ EventHandler = (*ingester)(nil)

type ingester struct {
	objectResolver *objects.ObjectResolver
	notifyQueue    queue.Queuer
	pool           *pgxpool.Pool
	log            *slog.Logger
	recordChan     chan<- batch.InsertRecord
	clusterName    string
	eventbus       eventbus.Bus
}

func (ing *ingester) Handle(obj *unstructured.Unstructured, op Operation) {
	// ref := &corev1.ObjectReference{
	// 	Kind:            obj.GetKind(),
	// 	APIVersion:      obj.GetAPIVersion(),
	// 	Name:            obj.GetName(),
	// 	Namespace:       obj.GetNamespace(),
	// 	UID:             obj.GetUID(),
	// 	ResourceVersion: obj.GetResourceVersion(),
	// }
	// compositionId, err := findCompositionID(ing.objectResolver, ref, ing.log)
	// if err != nil {
	// 	if !errors.Is(err, ErrCompositionIdNotFound) {
	// 		ing.log.Error("unable to look for composition id",
	// 			slog.String("obj", ref.Name),
	// 			slog.Any("err", err),
	// 		)
	// 		return
	// 	}
	// }

	// ing.log.Debug("Handling informer event for object",
	// 	slog.String("name", obj.GetName()),
	// 	slog.String("apiversion", obj.GetAPIVersion()),
	// 	slog.String("kind", obj.GetKind()),
	// 	slog.String("compositionId", compositionId),
	// )

	ing.eventbus.PublishAsync(context.Background(), InformerEvent{
		Name:      obj.GetName(),
		EventType: op,
		Obj:       *obj,
	})

	/*rec := ing.buildRecord(obj, compositionId)
	if rec.UID == "" {
		return
	}

	job := &batch.InsertRecordJob{
		Record: rec,
		Input:  ing.recordChan,
	}

	ing.notifyQueue.Push(job)
	*/
}

/*
func (ing *ingester) buildRecord(obj unstructured.Unstructured, compositionID string) batch.InsertRecord {
	// Choose best timestamp
	created := obj.EventTime.Time
	if created.IsZero() {
		created = obj.CreationTimestamp.Time
	}

	// Minify and label
	labels := out.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[keyCompositionID] = compositionID
	out.SetLabels(labels)

	raw, err := json.Marshal(out)
	if err != nil {
		ing.log.Error("failed to encode event as JSON", slog.Any("err", err))
		return batch.InsertRecord{}
	}

	api := obj.InvolvedObject.APIVersion
	kind := obj.InvolvedObject.Kind
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

	return batch.InsertRecord{
		CreatedAt:       created,
		ClusterName:     ing.clusterName,
		UID:             string(obj.UID),
		GlobalUID:       fmt.Sprintf("%s:%s", ing.clusterName, obj.UID),
		Namespace:       obj.Namespace,
		ResourceKind:    resourceKind,
		ResourceName:    obj.InvolvedObject.Name,
		EventType:       obj.Type,
		Reason:          obj.Reason,
		Message:         obj.Message,
		CompositionID:   cid,
		ResourceVersion: obj.ResourceVersion,
		Raw:             raw,
	}
}*/
