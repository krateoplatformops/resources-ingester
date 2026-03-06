package manager

import (
	"context"
	"log/slog"
	"time"

	"github.com/krateoplatformops/plumbing/eventbus"
	"github.com/krateoplatformops/resources-ingester/internal/router"
	"github.com/krateoplatformops/resources-ingester/internal/util"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
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
	}, nil
}

type manager struct {
	eventbus       eventbus.Bus
	dynamicClient  *dynamic.DynamicClient
	log            *slog.Logger
	handler        router.EventHandler
	resyncInterval time.Duration
	throttlePeriod time.Duration

	// Multiple namespaces or nil -> watch everything
	Namespaces []string
	informers  map[string]context.CancelFunc
}

func (m *manager) Run(stop <-chan struct{}) {
	defer utilruntime.HandleCrash()
	sub := m.eventbus.Subscribe(router.InformerEvent{}.EventID(), func(ctx context.Context, event eventbus.Event) error {
		eI := event.(router.InformerEvent)
		//m.log.Debug("Manager received event on eventbus", "obj", eI.Name, "kind", eI.Obj.GetKind())
		if eI.Obj.GetKind() == "CustomResourceDefinition" {
			gvrs, namespaced := util.ExtractGvrNsFromCrd(eI.Obj)
			// Gvrs will differ only in version, we can check the first one's group for compositions
			if len(gvrs) > 0 {
				if gvrs[0].Group != "composition.krateo.io" {
					return nil
				}
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
					ctx, cancel := context.WithCancel(context.Background())
					if !m.doesGvrExist(gvr.String()) && !gvr.Empty() {
						crdsRouter := router.NewRouter(router.RouterOpts{
							DynamicClient:  m.dynamicClient,
							Log:            m.log,
							Handler:        m.handler,
							ResyncInterval: m.throttlePeriod,
							ThrottlePeriod: m.throttlePeriod,
							Namespaces:     namespaces,
							Gvr:            gvr,
						})
						m.informers[gvr.String()] = cancel
						go crdsRouter.Run(ctx.Done())
					}
				case router.DELETE:
					m.log.Debug("Stopping router", "gvr", gvr.String())
					if cancel, ok := m.informers[gvr.String()]; ok {
						cancel()
						delete(m.informers, gvr.String())
					}
					m.log.Info("Stopped router", "gvr", gvr.String())
				case router.UPDATE:
					// Delete and recreate?
					// Prob. not necessary 1since its a dynamic informer with unstructured
				}
			}

		} else {

		}

		return nil
	})
	defer m.eventbus.Unsubscribe(sub)

	m.log.Info("Manager started")
	<-stop
	// Cancel all informers on stop
	for gvr := range m.informers {
		if cancel, ok := m.informers[gvr]; ok {
			cancel()
			delete(m.informers, gvr)
		}
	}
	m.log.Info("Manager stopped")
}

func (m *manager) GetInformers() int {
	return len(m.informers)
}

func (m *manager) doesGvrExist(gvr string) bool {
	_, ok := m.informers[gvr]
	return ok
}
