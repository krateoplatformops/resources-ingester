# Resources-Ingester Metrics Reference
This document describes the OpenTelemetry metrics emitted by `resources-ingester`.
## Naming note
In code, metric names use dots (for example `resources_ingester.resources.received`).
In Prometheus, names are typically normalized with underscores (for example `resources_ingester_resources_received`), and counters may be exposed with `_total`.
## Metrics
| Metric | Type | Unit | Description | Labels | Emitted from | PromQL example |
|---|---|---|---|---|---|---|
| `resources_ingester.resources.received` | Counter | count | Number of resources accepted by router input path. | none | `internal/router/router.go` | `sum(rate(resources_ingester_resources_received_total[5m])) or sum(rate(resources_ingester_resources_received[5m]))` |
| `resources_ingester.resources.dispatched` | Counter | count | Number of resources forwarded to the ingester handler. | none | `internal/router/router.go` | `sum(rate(resources_ingester_resources_dispatched_total[5m])) or sum(rate(resources_ingester_resources_dispatched[5m]))` |
| `resources_ingester.resources.dropped` | Counter | count | Dropped resources in router/ingester paths. | `reason` | `internal/router/router.go`, `internal/router/handler.go` | `sum by (reason) (rate(resources_ingester_resources_dropped_total[5m])) or sum by (reason) (rate(resources_ingester_resources_dropped[5m]))` |
| `resources_ingester.batch.flush.duration_seconds` | Histogram | seconds | Duration of a batch flush cycle. | none | `internal/batch/worker.go` | `histogram_quantile(0.95, sum by (le) (rate(resources_ingester_batch_flush_duration_seconds_bucket[5m])))` |
| `resources_ingester.batch.flush.size` | Histogram | records | Number of records per flush. | none | `internal/batch/worker.go` | `histogram_quantile(0.95, sum by (le) (rate(resources_ingester_batch_flush_size_bucket[5m])))` |
| `resources_ingester.db.insert.rows` | Counter | rows | Number of rows inserted by successful batch writes. | none | `internal/batch/worker.go` | `sum(rate(resources_ingester_db_insert_rows_total[5m])) or sum(rate(resources_ingester_db_insert_rows[5m]))` |
| `resources_ingester.db.insert.failure` | Counter | count | Batch insert failures. | `type` | `internal/batch/worker.go` | `sum by (type) (increase(resources_ingester_db_insert_failure_total[1h])) or sum by (type) (increase(resources_ingester_db_insert_failure[1h]))` |
| `resources_ingester.queue.depth` | Gauge | count | In-memory queue buffered job count. | none | `main.go` | `max(resources_ingester_queue_depth)` |
| `resources_ingester.record_channel.depth` | Gauge | count | In-memory record channel buffered item count. | none | `main.go` | `max(resources_ingester_record_channel_depth)` |

## Label cardinality guidance
- Keep labels low-cardinality and bounded (`reason`, `type`).
- Avoid dynamic labels such as `uid`, `resource_name`, `message`.
- Prefer service-level observability over per-resource dimensions.