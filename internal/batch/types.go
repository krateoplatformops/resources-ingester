package batch

import v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type InsertRecord struct {
	ClusterName       string
	UID               string
	GlobalUID         string
	Namespace         string
	ResourceKind      string
	ResourceName      string
	CompositionID     string
	Raw               []byte
	ResourceVersion   string
	DeletionTimestamp *v1.Time
}
