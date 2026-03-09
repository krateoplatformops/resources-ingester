package manager

import (
	"context"
	"sync"
)

func stopInformer(informers map[string]context.CancelFunc, gvr string, mu *sync.RWMutex) {
	if cancel, ok := informers[gvr]; ok {
		mu.Lock()
		cancel()
		delete(informers, gvr)
		mu.Unlock()
	}
}
