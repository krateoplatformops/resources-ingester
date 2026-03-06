package manager

import (
	"context"
	"errors"
	"log/slog"

	"github.com/krateoplatformops/plumbing/eventbus"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
)

type ManagerOpts struct {
	Eventbus eventbus.Bus
	Log      *slog.Logger
}

func NewManager(opts ManagerOpts) (manager, error) {
	return manager{
		eventbus: opts.Eventbus,
		log:      opts.Log,
	}, nil
}

type manager struct {
	eventbus eventbus.Bus
	log      *slog.Logger
}

func (m *manager) Run(stop <-chan struct{}) {
	defer utilruntime.HandleCrash()
	handlerErr := errors.New("manager failed")

	sub := m.eventbus.Subscribe(InformerCreateEventCrd{}.EventID(), func(ctx context.Context, event eventbus.Event) error {
		eI := event.(InformerCreateEventCrd)
		m.log.Info("Manager received event on eventbus", "obj", eI.Name)
		return handlerErr
	})
	defer m.eventbus.Unsubscribe(sub)

	m.log.Info("Manager started")
	<-stop
	m.log.Info("Manager stopped")
}
