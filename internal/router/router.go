package router

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

const (
	listPageSize   = 1000
	defaultWorkers = 1
)

func NewSharedQueue(throttlePeriod time.Duration) workqueue.TypedRateLimitingInterface[string] {
	var rl workqueue.TypedRateLimiter[string]
	if throttlePeriod > 0 {
		rl = workqueue.NewTypedItemExponentialFailureRateLimiter[string](throttlePeriod, 5*time.Minute)
	} else {
		rl = workqueue.DefaultTypedControllerRateLimiter[string]()
	}
	return workqueue.NewTypedRateLimitingQueue(rl)
}

type EventHandler interface {
	Handle(o *unstructured.Unstructured, op Operation, tg Target)
}

type Router struct {
	handler    EventHandler
	informers  []cache.SharedInformer
	log        *slog.Logger
	namespaces []string
	Gvr        schema.GroupVersionResource
	workers    int
	queue      workqueue.TypedRateLimitingInterface[string]
	mu         sync.Mutex
	pending    map[string]*pendingEvent
	wgpool     *WorkerPool
	target     Target
}

type RouterOpts struct {
	DynamicClient  *dynamic.DynamicClient
	Log            *slog.Logger
	Handler        EventHandler
	ResyncInterval time.Duration
	Queue          workqueue.TypedRateLimitingInterface[string]
	Workers        int
	Namespaces     []string
	Gvr            schema.GroupVersionResource
	Resource       string
	WgPool         *WorkerPool
	Target         Target
}

func NewRouter(opts RouterOpts) *Router {
	namespaces := opts.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{corev1.NamespaceAll}
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = defaultWorkers
	}

	opts.Log.Debug("NewRouter: creating informers",
		"gvr", opts.Gvr.String(),
		"namespaces", namespaces,
		"listPageSize", listPageSize,
		"workers", workers,
	)

	tweakListOptions := func(o *metav1.ListOptions) {
		o.Limit = listPageSize
	}

	var informers []cache.SharedInformer
	for _, ns := range namespaces {
		opts.Log.Debug("NewRouter: creating informer for namespace",
			"gvr", opts.Gvr.String(), "namespace", ns)

		f := dynamicinformer.NewFilteredDynamicSharedInformerFactory(
			opts.DynamicClient, opts.ResyncInterval, ns, tweakListOptions,
		)
		informers = append(informers, f.ForResource(opts.Gvr).Informer())
	}

	opts.Log.Debug("NewRouter: informers created",
		"gvr", opts.Gvr.String(), "count", len(informers))

	if opts.Queue == nil {
		panic("router: RouterOpts.Queue must not be nil")
	}

	return &Router{
		informers:  informers,
		handler:    opts.Handler,
		log:        opts.Log,
		namespaces: opts.Namespaces,
		Gvr:        opts.Gvr,
		workers:    workers,
		queue:      opts.Queue,
		wgpool:     opts.WgPool,
		pending:    make(map[string]*pendingEvent),
		target:     opts.Target,
	}
}

func (r *Router) Run(stop <-chan struct{}) {
	defer utilruntime.HandleCrash()

	r.log.Info("Router.Run: starting",
		"gvr", r.Gvr.String(),
		"informers", len(r.informers),
		"workers", r.workers,
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

	r.log.Info("Router.Run: caches synced",
		"gvr", r.Gvr.String(), "workers", r.workers)

	r.log.Info("Router started", "gvr", r.Gvr.String())
	<-stop
	r.log.Info("Router stopped", "gvr", r.Gvr.String())
}

func (r *Router) enqueue(obj *unstructured.Unstructured, op Operation) {
	if r.wgpool != nil {
		r.wgpool.Enqueue(r.handler, obj, op, r.target, r.Gvr)
	} else {
		r.handler.Handle(obj, op, r.target)
	}
}

func (r *Router) onAdd(obj any) {
	objUn, ok := obj.(*unstructured.Unstructured)
	if !ok {
		r.log.Error("onAdd: unexpected object type", "type", fmt.Sprintf("%T", obj))
		return
	}

	r.enqueue(objUn, CREATE)
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

	r.enqueue(newObjUn, UPDATE)
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

	r.enqueue(objUn, DELETE)
}
