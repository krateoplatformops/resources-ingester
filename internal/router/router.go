package router

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

const (
	listPageSize = 1000
)

type EventHandler interface {
	Handle(o *unstructured.Unstructured, op Operation, tg Target, kind string)
}

type Router struct {
	handler    EventHandler
	informers  []cache.SharedInformer
	log        *slog.Logger
	namespaces []string
	Gvr        schema.GroupVersionResource
	kind       string
	queue      workqueue.TypedRateLimitingInterface[string]
	mu         sync.Mutex
	wgpool     *WorkerPool
}

type RouterOpts struct {
	DynamicClient  *dynamic.DynamicClient
	Log            *slog.Logger
	Handler        EventHandler
	ResyncInterval time.Duration
	Queue          workqueue.TypedRateLimitingInterface[string]
	Namespaces     []string
	Gvr            schema.GroupVersionResource
	WgPool         *WorkerPool
	Kind           string
}

func NewRouter(opts RouterOpts) *Router {
	namespaces := opts.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{metav1.NamespaceAll}
	}

	var informers []cache.SharedInformer
	tweakListOptions := func(o *metav1.ListOptions) {
		o.Limit = listPageSize
	}
	for _, ns := range namespaces {
		f := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
			opts.DynamicClient,
			opts.ResyncInterval,
			ns,
			tweakListOptions,
		)

		inf := f.ForResource(opts.Gvr).Informer()
		informers = append(informers, inf)

		if opts.WgPool != nil {
			opts.WgPool.RegisterInformer(opts.Gvr, ns, opts.Kind, inf)
		}
	}

	return &Router{
		informers:  informers,
		handler:    opts.Handler,
		log:        opts.Log,
		Gvr:        opts.Gvr,
		queue:      opts.Queue,
		namespaces: namespaces,
		wgpool:     opts.WgPool,
		kind:       opts.Kind,
	}
}

func (r *Router) Run(stop <-chan struct{}) {
	defer utilruntime.HandleCrash()

	r.log.Info("Router.Run: starting",
		"gvr", r.Gvr.String(),
		"informers", len(r.informers),
	)

	for i, inf := range r.informers {
		inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    r.onAdd,
			UpdateFunc: r.onUpdate,
			DeleteFunc: r.onDelete,
		})
		go inf.Run(stop)
		r.log.Debug("Router.Run: informer goroutine launched",
			"gvr", r.Gvr.String(), "informer_index", i)
	}

	syncFuncs := make([]cache.InformerSynced, len(r.informers))
	for i, inf := range r.informers {
		syncFuncs[i] = inf.HasSynced
	}

	r.log.Info("Router.Run: waiting for caches to sync", "gvr", r.Gvr.String())

	if !cache.WaitForCacheSync(stop, syncFuncs...) {
		r.log.Info("Router.Run: stop signal received before cache sync completed",
			"gvr", r.Gvr.String())
		return
	}
	r.log.Info("router started", "gvr", r.Gvr.String())
	<-stop
	r.log.Info("Router stopped", "gvr", r.Gvr.String())
}

func (r *Router) enqueue(obj any) {
	fullKey, err := buildKey(r.Gvr, r.kind, obj)
	if err != nil {
		r.log.Error("could not build object key", "error", err)
		return
	}

	objUn := obj.(*unstructured.Unstructured)
	if objUn.GetDeletionTimestamp() != nil && r.wgpool != nil {
		r.wgpool.addDeletedObject(fullKey, objUn)
	}

	r.log.Debug("Adding to queue", "fullKey", fullKey)
	r.queue.Add(fullKey)
}

func (r *Router) onAdd(obj any) {
	objUn, ok := obj.(*unstructured.Unstructured)
	if !ok {
		r.log.Error("onAdd: unexpected object type", "type", fmt.Sprintf("%T", obj))
		return
	}

	r.enqueue(objUn)
}

func (r *Router) onUpdate(oldObj, newObj any) {
	oldObjUn, ok1 := oldObj.(*unstructured.Unstructured)
	newObjUn, ok2 := newObj.(*unstructured.Unstructured)
	if !ok1 || !ok2 {
		return
	}

	if oldObjUn.GetResourceVersion() == newObjUn.GetResourceVersion() {
		return
	}

	r.enqueue(newObjUn)
}

func (r *Router) onDelete(obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}
	objUn, ok := obj.(*unstructured.Unstructured)
	if !ok {
		r.log.Error("onDelete: unexpected object type", "type", fmt.Sprintf("%T", obj))
		return
	}

	fullKey, err := buildKey(r.Gvr, r.kind, obj)
	if err != nil {
		r.log.Error("could not build object key", "error", err)
		return
	}

	if r.wgpool != nil {
		r.wgpool.addDeletedObject(fullKey, objUn)
	}

	r.enqueue(objUn)
}

func (r *Router) InformersByNamespace() map[string]cache.SharedInformer {
	result := make(map[string]cache.SharedInformer, len(r.namespaces))
	for i, ns := range r.namespaces {
		result[ns] = r.informers[i]
	}
	return result
}
