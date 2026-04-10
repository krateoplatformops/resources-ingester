package batch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krateoplatformops/resources-ingester/internal/telemetry"
)

type Worker struct {
	pool       *pgxpool.Pool
	log        *slog.Logger
	input      <-chan InsertRecord
	buffer     []InsertRecord
	maxBatch   int
	flushEvery time.Duration
	columns    []string
	metrics    *telemetry.Metrics
}

type WorkerOpts struct {
	Pool       *pgxpool.Pool
	Log        *slog.Logger
	Input      <-chan InsertRecord
	MaxBatch   int
	FlushEvery time.Duration
	Metrics    *telemetry.Metrics
}

// NewWorker creates a batch worker
func NewWorker(opts WorkerOpts) *Worker {
	cols := MustLoadSQL("cols.sql") // list of columns
	return &Worker{
		pool:       opts.Pool,
		log:        opts.Log,
		input:      opts.Input,
		maxBatch:   opts.MaxBatch,
		flushEvery: opts.FlushEvery,
		columns:    parseColumns(cols),
		buffer:     make([]InsertRecord, 0, opts.MaxBatch),
		metrics:    opts.Metrics,
	}
}

func parseColumns(s string) []string {
	lines := strings.Split(s, ",")
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		c := strings.TrimSpace(l)
		if c != "" {
			out = append(out, c)
		}
	}
	return out
}

// Run starts the batch loop
func (w *Worker) Run(stop <-chan struct{}) {
	w.log.Info("Batch worker started")

	ticker := time.NewTicker(w.flushEvery)
	defer ticker.Stop()

	for {
		select {
		case rec := <-w.input:
			w.buffer = append(w.buffer, rec)

			if len(w.buffer) >= w.maxBatch {
				w.flush()
			}

		case <-ticker.C:
			if len(w.buffer) > 0 {
				w.flush()
			}

		case <-stop:
			w.log.Info("Batch worker stopping...")
			if len(w.buffer) > 0 {
				w.flush()
			}
			return
		}
	}
}

func (w *Worker) flush() {
	if len(w.buffer) == 0 {
		return
	}
	started := time.Now()
	batchSize := len(w.buffer)
	ctx := context.Background()
	defer func() { w.metrics.RecordBatchFlush(ctx, time.Since(started), batchSize) }()

	w.sync()
	//w.delete()

	w.log.Debug("flushed batch", slog.Int("count", len(w.buffer)))

	w.buffer = w.buffer[:0] // reset buffer
}

func (w *Worker) sync() {
	ctx := context.Background()
	batchSize := len(w.buffer)

	// Deduplicate: last write for a given global_uid wins
	seen := make(map[string]InsertRecord, len(w.buffer))
	for _, r := range w.buffer {
		seen[r.GlobalUID] = r
	}
	records := make([]InsertRecord, 0, len(seen))
	for _, r := range seen {
		records = append(records, r)
	}

	var b strings.Builder
	b.WriteString("INSERT INTO krateo_resources (")
	b.WriteString(strings.Join(w.columns, ", "))
	b.WriteString(") VALUES ")
	vals := make([]any, 0, len(records)*len(w.columns))
	firstRow := true
	for _, r := range records {
		if !firstRow {
			b.WriteString(", ")
		}
		firstRow = false
		b.WriteString("(")
		for j := range w.columns {
			if j > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "$%d", len(vals)+j+1)
		}
		b.WriteString(")")
		var da *time.Time
		if r.DeletedAt != nil {
			das := r.DeletedAt.UTC()
			da = &das
		}
		cas := r.CreatedAt.UTC()
		ca := &cas
		uas := r.UpdatedAt.UTC()
		ua := &uas
		vals = append(vals, ca, ua, da, r.ClusterName, r.UID, r.GlobalUID,
			r.Namespace, r.ResourceGroup, r.ResourceVersion, r.ResourceKind, r.ResourcePlural, r.ResourceName,
			r.CompositionID, r.Raw, r.StatusRaw,
		)
	}
	b.WriteString(`
		ON CONFLICT (global_uid) DO UPDATE SET
			deleted_at = EXCLUDED.deleted_at,
			raw = EXCLUDED.raw
	`)
	if len(vals) == 0 {
		w.log.Debug("nothing to sync, skipping")
		return
	}
	_, err := w.pool.Exec(ctx, b.String(), vals...)
	if err != nil {
		w.metrics.IncDBInsertFailure(ctx, "error")
		w.log.Error("batch sync failed", slog.Any("err", err))
	} else {
		w.metrics.AddDBInsertRows(ctx, int64(batchSize))
	}
}

func (w *Worker) delete() {
	ctx := context.Background()
	var b strings.Builder

	b.WriteString("DELETE FROM k8s_resources WHERE global_uid IN (")

	vals := make([]any, 0, len(w.buffer))
	paramIndex := 1
	for i, r := range w.buffer {
		if r.DeletedAt != nil {
			if paramIndex > 1 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "$%d", paramIndex)
			vals = append(vals, r.GlobalUID)
			paramIndex++
		}
		_ = i
	}

	b.WriteString(")")

	if len(vals) == 0 {
		w.log.Debug("nothing to delete, skipping")
		return
	}

	sql := b.String()
	_, err := w.pool.Exec(ctx, sql, vals...)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			w.log.Debug("duplicate key ignored", slog.Any("err", pgErr))
		} else {
			w.log.Error("batch delete failed", slog.Any("err", err))
		}
	}
}
