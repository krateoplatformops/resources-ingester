package router

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

func splitKey(full string) (string, string, error) {
	parts := strings.SplitN(full, "|", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid key: %s", full)
	}
	return parts[0], parts[1], nil
}

func buildKey(gvr schema.GroupVersionResource, key string) string {
	return fmt.Sprintf("%s/%s/%s|%s",
		gvr.Group,
		gvr.Version,
		gvr.Resource,
		key,
	)
}
