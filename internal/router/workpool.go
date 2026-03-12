package router

import (
	"fmt"
	"log/slog"
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
	delObjs   sync.Map
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
		delObjs:   sync.Map{},
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

	p.log.Debug("WorkerPool.processNext: took", slog.Int64("time ms", current.Milliseconds()))
	return true
}

func (p *WorkerPool) addDeletedObject(fullKey string, objUn *unstructured.Unstructured) {
	p.delObjs.Store(fullKey, objUn)
}

func (p *WorkerPool) reconcile(fullKey string) error {
	g, v, r, k, ns, n, err := splitKey(fullKey)
	if err != nil {
		return err
	}

	target := STORAGE
	if g == "apiextensions.k8s.io" {
		target = MANAGER
	}

	gvr := schema.GroupVersionResource{
		Group:    g,
		Version:  v,
		Resource: r,
	}
	p.mu.RLock()
	informerKey := getInformerKey(gvr, k, ns)
	informer, ok := p.informers[informerKey]
	// infKeys := make([]string, len(p.informers))
	// i := 0
	// for k := range p.informers {
	// 	infKeys[i] = k
	// 	i++
	// }
	p.mu.RUnlock()
	if !ok {
		informerKey = getInformerKey(gvr, k, "")
		informer, ok = p.informers[informerKey]
		if !ok {
			p.log.Error("no informer", "gvr", informerKey)
			// p.log.Debug("informer keys", "list", infKeys)
			return fmt.Errorf("no informer for %s in namespace %q", gvr, ns)
		}
	}

	obj, exists, err := informer.GetStore().GetByKey(getObjectKey(ns, n))
	if err != nil {
		p.log.Warn("no object in store", "exists", exists, "err", err)
		return err
	}
	if !exists {
		val, ok := p.delObjs.Load(fullKey)
		if !ok {
			p.log.Error("deleted object not in storage", "fullkey", fullKey, "ok", ok)
			return fmt.Errorf("deleted object not found for key %s", fullKey)
		}
		objUn := (val.(*unstructured.Unstructured))
		p.delObjs.Delete(fullKey)
		p.handler.Handle(objUn, DELETE, target, r)
		return nil
	}

	un := obj.(*unstructured.Unstructured)
	p.handler.Handle(un, UPDATE, target, r)
	return nil
}
