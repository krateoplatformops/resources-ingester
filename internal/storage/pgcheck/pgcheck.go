package pgcheck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/krateoplatformops/plumbing/pgutil"
	"github.com/krateoplatformops/plumbing/wait"
)

var (
	ErrTableNotFound  = errors.New("table krateo_resources does not exist")
	ErrSchemaMismatch = errors.New("table krateo_resources schema mismatch")
)

type SchemaMismatchError struct {
	Missing  []string
	Extra    []string
	Mistyped []string
}

func (e *SchemaMismatchError) Error() string {
	return fmt.Sprintf("%s: missing=%v extra=%v mistyped=%v",
		ErrSchemaMismatch, e.Missing, e.Extra, e.Mistyped)
}
func (e *SchemaMismatchError) Unwrap() error { return ErrSchemaMismatch }

var expectedColumns = map[string]string{
	"created_at":       "timestamp with time zone",
	"updated_at":       "timestamp with time zone",
	"deleted_at":       "timestamp with time zone",
	"id":               "bigint",
	"cluster_name":     "text",
	"uid":              "text",
	"global_uid":       "text",
	"namespace":        "text",
	"resource_group":   "text",
	"resource_version": "text",
	"resource_kind":    "text",
	"resource_plural":  "text",
	"resource_name":    "text",
	"composition_id":   "uuid",
	"raw":              "jsonb",
	"status_raw":       "jsonb",
}

const tableName = "krateo_resources"

// WaitForPostgres is a drop-in replacement for pgutil.WaitForPostgres that
// additionally verifies that krateo_resources exists with the expected schema.
func WaitForPostgres(ctx context.Context, log *slog.Logger, dbURL string) (*pgxpool.Pool, error) {
	return WaitForPostgresWithOptions(ctx, dbURL, wait.Options{Logger: log})
}

// WaitForPostgresWithOptions delegates connectivity to the upstream library,
// then adds table-existence and schema-validation checks.
func WaitForPostgresWithOptions(
	ctx context.Context,
	dbURL string,
	opts wait.Options,
) (*pgxpool.Pool, error) {
	// Phase 1 – reuse the upstream library for the connectivity + ping check.
	pool, err := pgutil.WaitForPostgresWithOptions(ctx, dbURL, opts)
	if err != nil {
		return nil, err
	}

	// Phase 2 – wait until the table appears (retried, same opts budget).
	_, err = wait.UntilWithOptions(ctx,
		func(ctx context.Context) (*struct{}, error) {
			exists, checkErr := tableExists(ctx, pool, tableName)
			if checkErr != nil {
				return nil, checkErr
			}
			if !exists {
				return nil, ErrTableNotFound
			}
			return &struct{}{}, nil
		},
		opts)
	if err != nil {
		pool.Close()
		if errors.Is(err, ErrTableNotFound) {
			return nil, ErrTableNotFound
		}
		return nil, err
	}

	// Phase 3 – schema check (single shot; a wrong schema won't self-heal).
	if schemaErr := validateSchema(ctx, pool, tableName); schemaErr != nil {
		pool.Close()
		return nil, schemaErr
	}

	return pool, nil
}

func tableExists(ctx context.Context, pool *pgxpool.Pool, table string) (bool, error) {
	const q = `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.tables
			WHERE table_schema = current_schema()
			  AND table_name   = $1
		)`
	var exists bool
	if err := pool.QueryRow(ctx, q, table).Scan(&exists); err != nil {
		return false, fmt.Errorf("checking table existence: %w", err)
	}
	return exists, nil
}

func validateSchema(ctx context.Context, pool *pgxpool.Pool, table string) error {
	const q = `
		SELECT column_name, data_type
		FROM   information_schema.columns
		WHERE  table_schema = current_schema()
		  AND  table_name   = $1`

	rows, err := pool.Query(ctx, q, table)
	if err != nil {
		return fmt.Errorf("querying columns: %w", err)
	}
	defer rows.Close()

	live := make(map[string]string)
	for rows.Next() {
		var col, dtype string
		if err := rows.Scan(&col, &dtype); err != nil {
			return fmt.Errorf("scanning column row: %w", err)
		}
		live[col] = dtype
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterating column rows: %w", err)
	}

	var mismatch SchemaMismatchError
	for col, wantType := range expectedColumns {
		gotType, ok := live[col]
		if !ok {
			mismatch.Missing = append(mismatch.Missing, col)
			continue
		}
		if !strings.EqualFold(gotType, wantType) {
			mismatch.Mistyped = append(mismatch.Mistyped,
				fmt.Sprintf("%s (want %s, got %s)", col, wantType, gotType))
		}
	}
	for col := range live {
		if _, expected := expectedColumns[col]; !expected {
			mismatch.Extra = append(mismatch.Extra, col)
		}
	}

	if len(mismatch.Missing)+len(mismatch.Extra)+len(mismatch.Mistyped) > 0 {
		return &mismatch
	}
	return nil
}
