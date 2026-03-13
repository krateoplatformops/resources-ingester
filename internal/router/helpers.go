package router

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
)

func splitKey(full string) (string, string, string, string, string, string, error) {
	parts := strings.SplitN(full, "|", 2)
	if len(parts) != 2 {
		return "", "", "", "", "", "", fmt.Errorf("invalid key: %s", full)
	}
	ns, n, err := cache.SplitMetaNamespaceKey(parts[1])
	if err != nil {
		return "", "", "", "", "", "", nil
	}
	gvrk := strings.Split(parts[0], "/")
	gr := gvrk[0]
	v := gvrk[1]
	r := gvrk[2]
	k := gvrk[3]
	return gr, v, r, k, ns, n, nil
}

func buildKey(gvr schema.GroupVersionResource, kind string, obj any) (string, error) {
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/%s/%s/%s|%s",
		gvr.Group,
		gvr.Version,
		gvr.Resource,
		kind,
		key,
	), nil
}

func getInformerKey(gvr schema.GroupVersionResource, kind string, ns string) string {
	if len(ns) > 0 {
		return fmt.Sprintf("%s/%s/%s/%s|%s", gvr.Group, gvr.Version, gvr.Resource, kind, ns)
	} else {
		return fmt.Sprintf("%s/%s/%s/%s|", gvr.Group, gvr.Version, gvr.Resource, kind)
	}
}

// Mimics cache.MetaNamespaceKeyFunc(obj) behaviour, without the obj
func getObjectKey(ns string, n string) string {
	if len(ns) > 0 {
		return fmt.Sprintf("%s/%s", ns, n)
	}
	return n
}

//go:embed assets/static
var static embed.FS

func MustLoad(filename string) string {
	if !strings.HasPrefix(filename, "assets/") {
		filename = "assets/" + filename
	}

	b, err := static.ReadFile(filename)
	if err != nil {
		panic(err)
	}

	return strings.TrimSpace(string(b))
}

func getRoutersFromStatic(static string) []map[string]string {
	rows := strings.Split(static, "\n")
	result := []map[string]string{}
	for _, row := range rows {
		p := strings.Split(row, "/")
		if len(p) != 4 {
			continue
		}
		pm := map[string]string{}
		pm["group"] = p[0]
		pm["version"] = p[1]
		pm["resource"] = p[2]
		pm["kind"] = p[3]
		result = append(result, pm)
	}
	return result
}

func NewStaticRouters(ctx context.Context, client *dynamic.DynamicClient, log *slog.Logger, ing EventHandler, q workqueue.TypedRateLimitingInterface[QueueItem], wgpool *WorkerPool, statics string) {
	for _, mp := range getRoutersFromStatic(statics) {
		g := mp["group"]
		v := mp["version"]
		r := mp["resource"]
		k := mp["kind"]

		crdsRouter := NewRouter(RouterOpts{
			DynamicClient:  client,
			Log:            log,
			Handler:        ing,
			ResyncInterval: 8 * time.Hour, // TODO make configurable
			WgPool:         nil,
			Queue:          q,
			Kind:           k,
			Namespaces:     []string{},
			Gvr: schema.GroupVersionResource{
				Group:    g,
				Version:  v,
				Resource: r,
			},
		})
		for ns, inf := range crdsRouter.InformersByNamespace() {
			wgpool.RegisterInformer(schema.GroupVersionResource{
				Group:    g,
				Version:  v,
				Resource: r,
			}, ns, k, inf)
		}
		go crdsRouter.Run(ctx.Done())
	}
}
