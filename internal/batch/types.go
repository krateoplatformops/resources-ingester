package batch

import (
	"github.com/jackc/pgx/v5/pgtype"
)

type InsertRecord struct {
	ClusterName     string
	UID             string
	GlobalUID       string
	Namespace       string
	ResourceKind    string
	ResourceName    string
	CompositionID   pgtype.UUID
	Raw             []byte
	ResourceVersion string
}
