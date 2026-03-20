package batch

import v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type InsertRecord struct {
	CreatedAt *v1.Time
	UpdatedAt *v1.Time
	DeletedAt *v1.Time

	ClusterName string
	UID         string
	GlobalUID   string

	Namespace       string
	ResourceGroup   string
	ResourceVersion string
	ResourceKind    string
	ResourceName    string
	ResourcePlural  string

	CompositionID string

	Raw       []byte
	StatusRaw []byte
}
