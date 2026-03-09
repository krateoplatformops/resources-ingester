package storage

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krateoplatformops/resources-ingester/internal/queue"
	corev1 "k8s.io/api/core/v1"
)

var _ queue.Jober = (*insertJob)(nil)

type insertJob struct {
	pool      *pgxpool.Pool
	log       *slog.Logger
	eventInfo corev1.Event
}

func (ij *insertJob) Job() {
	e := ij.eventInfo

	uid := string(e.UID)
	rv := e.ResourceVersion

	if uid == "" || rv == "" {
		ij.log.Warn("event missing UID or ResourceVersion, skipping",
			slog.String("name", e.Name),
		)
		return
	}

	raw, err := json.Marshal(e)
	if err != nil {
		ij.log.Error("unable to marshal event",
			slog.String("uid", uid),
			slog.Any("err", err))
		return
	}

	compositionId := ""
	if labels := e.GetLabels(); len(labels) > 0 {
		compositionId = labels[keyCompositionID]
	}

	_, err = ij.pool.Exec(
		context.Background(),
		`
INSERT INTO k8s_events (
    cluster_name,
    namespace,
    resource_kind,
    resource_name,
    composition_id,
    raw,
    uid,
    resource_version
) VALUES (
    $1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8
)
ON CONFLICT (uid, resource_version) DO NOTHING;
`,
		e.LastTimestamp.Time,
		"MyCluster", // TODO detect automatically or pass
		e.Namespace,
		e.InvolvedObject.Kind,
		e.InvolvedObject.Name,
		e.Type,
		e.Reason,
		e.Message,
		compositionId,
		raw,
		uid,
		rv,
	)

	if err != nil {
		ij.log.Error("failed DB insert",
			slog.String("uid", uid),
			slog.Any("err", err))
		return
	}

	ij.log.Debug("event inserted",
		slog.String("uid", uid),
		slog.String("resourceVersion", rv))
}
