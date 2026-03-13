package manager

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/krateoplatformops/plumbing/eventbus"
	"github.com/krateoplatformops/resources-ingester/internal/router"
	"github.com/krateoplatformops/resources-ingester/internal/util"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/workqueue"
)

const CRD_KIND = "CustomResourceDefinition"

type ManagerOpts struct {
	Eventbus       eventbus.Bus
	DynamicClient  *dynamic.DynamicClient
	Log            *slog.Logger
	Handler        router.EventHandler
	ResyncInterval time.Duration
	ThrottlePeriod time.Duration

	// Multiple namespaces or nil -> watch everything
	Namespaces []string
	Queue      workqueue.TypedRateLimitingInterface[router.QueueItem]
	WgPool     *router.WorkerPool
}

func NewManager(opts ManagerOpts) (manager, error) {
	return manager{
		eventbus:       opts.Eventbus,
		log:            opts.Log,
		handler:        opts.Handler,
		throttlePeriod: opts.ThrottlePeriod,
		resyncInterval: opts.ResyncInterval,
		dynamicClient:  opts.DynamicClient,
		Namespaces:     opts.Namespaces,
		informers:      make(map[string]context.CancelFunc),
		queue:          opts.Queue,
		mu:             &sync.RWMutex{},
		wgpool:         opts.WgPool,
		managedGroups:  parse(mustLoad("managed_groups")),
	}, nil
}

type manager struct {
	eventbus       eventbus.Bus
	dynamicClient  *dynamic.DynamicClient
	log            *slog.Logger
	handler        router.EventHandler
	resyncInterval time.Duration
	throttlePeriod time.Duration
	mu             *sync.RWMutex
	queue          workqueue.TypedRateLimitingInterface[router.QueueItem]
	wgpool         *router.WorkerPool

	// Multiple namespaces or nil -> watch everything
	Namespaces    []string
	informers     map[string]context.CancelFunc
	managedGroups map[string]struct{}
}

func (m *manager) Run(stop <-chan struct{}) {
	defer utilruntime.HandleCrash()
	mgrCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		<-stop
		cancel()
	}()

	sub := m.eventbus.Subscribe(router.InformerEvent{}.EventID(), func(ctx context.Context, event eventbus.Event) error {
		eI := event.(router.InformerEvent)
		if eI.EventTarget != router.MANAGER {
			return nil
		}
		m.log.Debug("Manager received event on eventbus", "obj", eI.Name, "kind", eI.Obj.GetKind())
		if eI.Obj.GetKind() == "CustomResourceDefinition" {
			gvrs, kind, namespaced := util.ExtractGvrkNsFromCrd(eI.Obj)
			// Gvrs will differ only in version, we can check the first one's group for compositions
			if len(gvrs) > 0 && !isManagedGroup(m.managedGroups, gvrs[0].Group) {
				return nil
			}

			m.log.Debug("Group match for compositions", "obj", eI.Name)

			namespaces := m.Namespaces
			if !namespaced {
				namespaces = []string{}
			}

			m.log.Debug("gvrs for composition", "obj", eI.Name, "len(gvrs)", len(gvrs), "op", eI.EventType)

			for _, gvr := range gvrs {
				switch eI.EventType {
				case router.CREATE:
					m.log.Debug("Starting router", "gvr", gvr.String())
					m.startInformer(mgrCtx, gvr, kind, namespaces)
				case router.DELETE:
					m.log.Debug("Stopping router", "gvr", gvr.String())
					stopInformer(m.informers, gvr.String(), m.mu)
					m.log.Info("Stopped router", "gvr", gvr.String())
				case router.UPDATE: // case DELETE then CREATE, if not DELETION TIMESTAMP
					if eI.Obj.GetDeletionTimestamp() != nil {
						m.log.Debug("Deletion timestamp on CRD, stopping router", "gvr", gvr.String())
						stopInformer(m.informers, gvr.String(), m.mu)
						m.log.Debug("Stopped router", "gvr", gvr.String())
					} else {
						m.log.Debug("Starting router", "gvr", gvr.String())
						m.startInformer(mgrCtx, gvr, kind, namespaces)
					}
				}
			}
		}
		return nil
	})
	defer m.eventbus.Unsubscribe(sub)

	m.log.Info("Manager started")
	<-stop
	m.log.Info("Manager stopped")
}

func (m *manager) GetInformers() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.informers)
}

func (m *manager) startInformer(parent context.Context, gvr schema.GroupVersionResource, kind string, namespaces []string) {
	if gvr.Empty() || gvr.Version == "vacuum" {
		return
	}

	m.mu.Lock()
	if _, ok := m.informers[gvr.String()]; ok {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.informers[gvr.String()] = cancel
	m.mu.Unlock()

	m.log.Debug("Starting router", "gvr", gvr.String())
	crdsRouter := router.NewRouter(router.RouterOpts{
		DynamicClient:  m.dynamicClient,
		Log:            m.log,
		Handler:        m.handler,
		ResyncInterval: m.resyncInterval,
		Queue:          m.queue,
		WgPool:         m.wgpool,
		Namespaces:     namespaces,
		Gvr:            gvr,
		Kind:           kind,
	})
	go crdsRouter.Run(ctx.Done())
}
