package router

import (
	"fmt"
	"log/slog"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/tools/cache"
)

type Operation string

const (
	CREATE Operation = "create"
	UPDATE Operation = "update"
	DELETE Operation = "delete"
)

// EventHandler handles final processed events
type EventHandler interface {
	Handle(o *unstructured.Unstructured, op Operation)
}

// Router routes Kubernetes Objects to a handler with throttling,
// deduplication and multi-namespace support.
type Router struct {
	handler        EventHandler
	informers      []cache.SharedInformer
	throttlePeriod time.Duration
	log            *slog.Logger

	namespaces []string
	Gvr        schema.GroupVersionResource
}

type RouterOpts struct {
	DynamicClient  *dynamic.DynamicClient
	Log            *slog.Logger
	Handler        EventHandler
	ResyncInterval time.Duration
	ThrottlePeriod time.Duration

	// Multiple namespaces or nil -> watch everything
	Namespaces []string
	Gvr        schema.GroupVersionResource
	Resource   string
}

func NewRouter(opts RouterOpts) *Router {
	namespaces := opts.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{corev1.NamespaceAll}
	}

	var informers []cache.SharedInformer
	for _, ns := range namespaces {
		f := dynamicinformer.NewFilteredDynamicSharedInformerFactory(opts.DynamicClient, 0, ns, nil)
		i := f.ForResource(opts.Gvr)
		informers = append(informers, i.Informer())
	}

	return &Router{
		informers:      informers,
		handler:        opts.Handler,
		throttlePeriod: opts.ThrottlePeriod,
		log:            opts.Log,
		namespaces:     opts.Namespaces,
		Gvr:            opts.Gvr,
	}
}

func (or *Router) Run(stop <-chan struct{}) {
	defer utilruntime.HandleCrash()

	for _, inf := range or.informers {
		inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc:    or.OnAdd,
			UpdateFunc: or.OnUpdate,
			DeleteFunc: or.OnDelete,
		})

		go inf.Run(stop)
	}

	// Wait for all informers to sync
	for _, inf := range or.informers {
		if !cache.WaitForCacheSync(stop, inf.HasSynced) {
			err := fmt.Errorf("timed out waiting for caches to sync")
			utilruntime.HandleError(err)
			or.log.Error("cache sync failed", slog.Any("err", err))
			return
		}
	}

	or.log.Info("Router started", "gvr", or.Gvr.String())
	<-stop
	or.log.Info("Router stopped", "gvr", or.Gvr.String())
}

func (or *Router) OnAdd(obj any) {
	objUn, ok := obj.(*unstructured.Unstructured)
	if !ok {
		or.log.Error("OnCreate: unexpected object type", "type", fmt.Sprintf("%T", obj))
		return
	}
	or.onEvent(objUn, CREATE)
}

// Dedup by ResourceVersion to prevent noisy updates
func (or *Router) OnUpdate(oldObj, newObj any) {
	oldObjUn, ok1 := oldObj.(*unstructured.Unstructured)
	newObjUn, ok2 := newObj.(*unstructured.Unstructured)
	if !ok1 || !ok2 {
		or.log.Error("OnUpdate: unexpected object type", "type old", fmt.Sprintf("%T", oldObj), "type new", fmt.Sprintf("%T", newObj))
		return
	}

	if oldObjUn.GetResourceVersion() == newObjUn.GetResourceVersion() {
		// No real change — skip
		return
	}

	or.onEvent(newObjUn, UPDATE)
}

func (or *Router) OnDelete(obj any) {
	if tombstone, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		obj = tombstone.Obj
	}

	objUn, ok := obj.(*unstructured.Unstructured)
	if !ok {
		or.log.Error("OnDelete: unexpected object type", "type", fmt.Sprintf("%T", obj))
		return
	}

	or.onEvent(objUn, DELETE)
}

func (or *Router) onEvent(objUn *unstructured.Unstructured, op Operation) {
	//or.log.Info("onEvent called", "obj", objUn.GetName(), "op", op)
	or.handler.Handle(objUn, op)
}
