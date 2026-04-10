package router

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/krateoplatformops/resources-ingester/internal/telemetry"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

func NewSharedQueue(throttlePeriod time.Duration) workqueue.TypedRateLimitingInterface[QueueItem] {
	var rl workqueue.TypedRateLimiter[QueueItem]
	if throttlePeriod > 0 {
		rl = workqueue.NewTypedItemExponentialFailureRateLimiter[QueueItem](throttlePeriod, 5*time.Minute)
	} else {
		rl = workqueue.DefaultTypedControllerRateLimiter[QueueItem]()
	}
	return workqueue.NewTypedRateLimitingQueue(rl)
}

type WorkerPool struct {
	queue     workqueue.TypedRateLimitingInterface[QueueItem]
	workers   int
	log       *slog.Logger
	handler   EventHandler
	informers map[string]cache.SharedInformer
	wg        sync.WaitGroup
	mu        sync.RWMutex
	metrics   *telemetry.Metrics
}

func NewWorkerPool(
	queue workqueue.TypedRateLimitingInterface[QueueItem],
	workers int,
	handler EventHandler,
	logger *slog.Logger,
	metrics *telemetry.Metrics,
) *WorkerPool {
	return &WorkerPool{
		queue:     queue,
		workers:   workers,
		handler:   handler,
		log:       logger,
		informers: map[string]cache.SharedInformer{},
		metrics:   metrics,
	}
}

func (p *WorkerPool) RegisterInformer(gvr schema.GroupVersionResource, ns string, kind string, inf cache.SharedInformer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.informers[getInformerKey(gvr, kind, ns)] = inf
}

func (p *WorkerPool) Start(stop <-chan struct{}) {
	for i := 0; i < p.workers; i++ {
		p.wg.Go(func() {
			p.runWorker()
		})
	}
	<-stop
}

func (p *WorkerPool) Wait() {
	p.queue.ShutDown()
	p.wg.Wait()
}

func (p *WorkerPool) runWorker() {
	for p.processNext() {
	}
}

func (p *WorkerPool) processNext() bool {
	start := time.Now()

	item, shutdown := p.queue.Get()
	if shutdown {
		return false
	}
	defer p.queue.Done(item)

	p.log.Debug("WorkerPool.processNext: dispatching", "key", item.Key)

	err := p.reconcile(item)
	if err != nil {
		if p.queue.NumRequeues(item) < 5 {
			p.queue.AddRateLimited(item)
			return true
		}
		p.log.Error("dropping key after max retries", "key", item.Key)
		p.queue.Forget(item)
		return true
	}

	p.queue.Forget(item)
	current := time.Since(start)
	p.log.Debug("WorkerPool.processNext: took", slog.Int64("time ms", current.Milliseconds()))
	p.metrics.IncResourcesDispatched(context.Background())
	return true
}

func (p *WorkerPool) reconcile(item QueueItem) error {
	g, v, r, k, ns, n, err := splitKey(item.Key)
	if err != nil {
		return err
	}
	if item.Object != nil {
		p.handler.Handle(item.Object, DELETE, STORAGE, r)
		return nil
	}

	target := STORAGE
	if g == "apiextensions.k8s.io" {
		target = MANAGER
	}

	gvr := schema.GroupVersionResource{Group: g, Version: v, Resource: r}

	p.mu.RLock()
	informerKey := getInformerKey(gvr, k, ns)
	informer, ok := p.informers[informerKey]
	p.mu.RUnlock()
	if !ok {
		informerKey = getInformerKey(gvr, k, "")
		informer, ok = p.informers[informerKey]
		if !ok {
			p.log.Error("no informer", "gvr", informerKey)
			return fmt.Errorf("no informer for %s in namespace %q", gvr, ns)
		}
	}

	obj, exists, err := informer.GetStore().GetByKey(getObjectKey(ns, n))
	if err != nil {
		p.log.Warn("no object in store", "exists", exists, "err", err)
		return err
	}
	if !exists {
		p.log.Debug("object no longer in store, skipping", "key", item.Key)
		return nil
	}

	un := obj.(*unstructured.Unstructured)
	p.handler.Handle(un, UPDATE, target, r)
	return nil
}
