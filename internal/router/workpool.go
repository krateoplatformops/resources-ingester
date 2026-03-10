package router

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
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

type WorkerPool struct {
	queue     workqueue.TypedRateLimitingInterface[string]
	workers   int
	log       *slog.Logger
	handler   EventHandler
	informers map[string]cache.SharedInformer
	wg        sync.WaitGroup
	mu        sync.RWMutex
	metrics   *WorkerPoolMetrics
}

func NewWorkerPool(
	queue workqueue.TypedRateLimitingInterface[string],
	workers int,
	handler EventHandler,
	logger *slog.Logger,
	metrics *WorkerPoolMetrics,
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

func (p *WorkerPool) RegisterInformer(gvr schema.GroupVersionResource, namespace string, inf cache.SharedInformer) {
	key := fmt.Sprintf("%s/%s/%s|%s", gvr.Group, gvr.Version, gvr.Resource, namespace)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.informers[key] = inf
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
	key, shutdown := p.queue.Get()

	p.log.Debug("WorkerPool.processNext: dispatching",
		"key", key,
	)
	if shutdown {
		return false
	}
	defer p.queue.Done(key)

	err := p.reconcile(key)
	if err != nil {
		p.metrics.errors.Inc()
		if p.queue.NumRequeues(key) < 5 {
			p.queue.AddRateLimited(key)
			return true
		}
		p.log.Error("dropping key after max retries", "key", key)
		p.queue.Forget(key)
		return true
	}

	p.queue.Forget(key)
	current := time.Since(start)
	p.metrics.processed.Inc()
	p.metrics.duration.Observe(current.Seconds())

	p.log.Debug("WorkerPool.processNext: took", "time", current.Milliseconds())
	return true
}

func (p *WorkerPool) reconcile(fullKey string) error {
	gvr, nsName, err := splitKey(fullKey)
	if err != nil {
		return err
	}

	target := STORAGE
	if strings.Split(gvr, "/")[0] == "apiextensions.k8s.io" {
		target = MANAGER
	}

	// nsName is "namespace/name" for namespaced or "name" for cluster-scoped
	ns, _, err := cache.SplitMetaNamespaceKey(nsName)
	if err != nil {
		return err
	}

	p.mu.RLock()
	informerKey := fmt.Sprintf("%s|%s", gvr, ns)
	informer, ok := p.informers[informerKey]
	if !ok {
		informer, ok = p.informers[fmt.Sprintf("%s|", gvr)]
	}
	p.mu.RUnlock()
	if !ok {
		p.log.Error("no informer", "gvr", gvr)
		return fmt.Errorf("no informer for %s in namespace %q", gvr, ns)
	}

	obj, exists, err := informer.GetStore().GetByKey(nsName)
	if err != nil {
		p.log.Warn("no object in store", "exists", exists, "err", err)
		return err
	}
	if !exists {
		p.handler.Handle(nil, DELETE, target)
		return nil
	}

	un := obj.(*unstructured.Unstructured)
	p.handler.Handle(un, UPDATE, target)
	return nil
}
