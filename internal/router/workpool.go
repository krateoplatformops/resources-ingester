package router

import (
	"log/slog"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

type pendingEvent struct {
	obj     *unstructured.Unstructured
	op      Operation
	tg      Target
	handler EventHandler
	gvr     schema.GroupVersionResource
}

type WorkerPool struct {
	queue   workqueue.TypedRateLimitingInterface[string]
	pending map[string]*pendingEvent
	mu      sync.Mutex
	wg      sync.WaitGroup
	log     *slog.Logger
	workers int
}

func NewWorkerPool(queue workqueue.TypedRateLimitingInterface[string], workers int, logger *slog.Logger) *WorkerPool {
	return &WorkerPool{
		queue:   queue,
		pending: make(map[string]*pendingEvent),
		workers: workers,
		log:     logger,
	}
}

func (p *WorkerPool) Start(stop <-chan struct{}) {
	for i := 0; i < p.workers; i++ {
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.runWorker()
		}()
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

func (p *WorkerPool) Enqueue(handler EventHandler, obj *unstructured.Unstructured, op Operation, tg Target, gvr schema.GroupVersionResource) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		p.log.Error("WorkerPool.Enqueue: could not build key", "err", err)
		return
	}

	fullKey := gvr.String() + "/" + key

	p.mu.Lock()
	p.pending[fullKey] = &pendingEvent{obj: obj, op: op, handler: handler, gvr: gvr, tg: tg}
	p.mu.Unlock()

	p.queue.Add(fullKey)
}

func (p *WorkerPool) processNext() bool {
	key, quit := p.queue.Get()
	if quit {
		return false
	}
	defer p.queue.Done(key)

	p.mu.Lock()
	event, exists := p.pending[key]
	delete(p.pending, key)
	p.mu.Unlock()

	if !exists {
		p.queue.Forget(key)
		return true
	}

	p.log.Debug("WorkerPool.processNext: dispatching",
		"gvr", event.gvr.String(),
		"key", key,
		"op", event.op,
	)

	event.handler.Handle(event.obj, event.op, event.tg)
	p.queue.Forget(key)
	return true
}

func (p *WorkerPool) Pending() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}
