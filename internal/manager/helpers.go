package manager

import (
	"context"
	"embed"
	"strings"
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

//go:embed assets/managed_groups
var static embed.FS

func mustLoad(filename string) string {
	if !strings.HasPrefix(filename, "assets/") {
		filename = "assets/" + filename
	}

	b, err := static.ReadFile(filename)
	if err != nil {
		panic(err)
	}

	return strings.TrimSpace(string(b))
}

func parse(content string) map[string]struct{} {
	groups := map[string]struct{}{}
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			groups[line] = struct{}{}
		}
	}
	return groups
}

func isManagedGroup(groups map[string]struct{}, group string) bool {
	_, ok := groups[group]
	return ok
}
