package router

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// QueueItem is the unit of work passed through the workqueue.
// For delete events, Object is populated directly so reconcile
// never needs to look it up from the store or a side-channel map.
// For add/update events, Object is nil and the store is used as normal.
type QueueItem struct {
	Key    string
	Object *unstructured.Unstructured // non-nil only for deletes
}
