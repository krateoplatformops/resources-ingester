package router

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
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
