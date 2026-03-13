# Resources Ingester

```txt
Kubernetes → ingester → PostgreSQL → presenter → Clients
```

The ingester focuses on **collection, normalization, and storage** of Kubernetes resources at scale.

## What Does It Do?

The service continuously watches Kubernetes resources across configured namespaces and transforms them into structured records stored in PostgreSQL.

Core responsibilities:

- Watch any Kubernetes resource kind using the in-cluster client
- Dynamically discover and watch CRDs belonging to managed API groups
- Resolve composition relationships for Krateo resources
- Enrich records with cluster metadata for multi-tenancy
- Batch inserts for high performance
- Push records into PostgreSQL
- Provide health probes for Kubernetes
- Handle graceful shutdown

## Architecture Overview

```
Kubernetes API
     ↓
Static Routers (fixed resource kinds)
     +
Dynamic Routers (CRD-driven, managed groups)
     ↓
Worker Pool (shared queue)
     ↓
Ingester (enrichment + routing)
     ↓
Batch Writer
     ↓
PostgreSQL
```

### Key Components

**Static Routers**

- Defined at compile time via `router/assets/static`
- Watch fixed resource kinds (Pods, Deployments, Namespaces, CRDs, etc.)
- Start immediately on service boot

**Dynamic Routers (Manager)**

- Watch `CustomResourceDefinition` events
- Dynamically start or stop routers when CRDs in managed API groups are created, updated, or deleted
- Managed groups are defined in `manager/assets/managed_groups`

## Stored Resource Model

Each Kubernetes resource is transformed into a structured record containing:

- Cluster name
- Namespace
- API group, version, kind, and plural resource name
- Resource name
- Composition ID (when available)
- Creation, update, and deletion timestamps
- Raw JSON payload (full object)
- UID and globally unique ID (`cluster:uid`)

Resources are stored with their full unstructured representation.

## Requirements

- Kubernetes cluster
- PostgreSQL
- Network connectivity between the service and the database
- RBAC permissions to watch the configured resource kinds

## Configuration

The application is configured via environment variables.

| Variable | Description | Default |
|---|---|---|
| `PORT` | Health probe server port | `8080` (implementation-dependent) |
| `DB_USER` | Database username | — |
| `DB_PASS` | Database password | — |
| `DB_NAME` | Database name | — |
| `DB_HOST` | Database host | — |
| `DB_PORT` | Database port | `5432` |
| `DB_PARAMS` | Extra connection parameters | — |
| `DB_READY_TIMEOUT` | Max wait for PostgreSQL readiness | `2m` |
| `NAMESPACES` | Namespaces to watch | all (if empty) |

> The service builds the PostgreSQL connection string from these values.

## Configuring Watched Resources

The ingester has two separate mechanisms for deciding which resources to watch.

### Static Routers

Static routers are defined in `router/assets/static`. Each line declares a resource kind to watch from the moment the service starts, regardless of CRDs.

**Format:** `group/version/resource/Kind`

For cluster-scoped resources and core API group resources, leave the group segment empty.

```txt
apiextensions.k8s.io/v1/customresourcedefinitions/CustomResourceDefinition
apps/v1/deployments/Deployment
/v1/pods/Pod
/v1/namespaces/Namespace
```

Rules:

- One entry per line, blank lines are ignored
- Exactly four `/`-separated segments are required; lines with a different count are skipped
- The group segment may be empty (core API group resources such as Pods and Namespaces)
- CRDs **must** be listed here for the dynamic manager to function, since it subscribes to CRD events

### Managed API Groups (Dynamic CRD Watching)

Dynamic routers are driven by CRD discovery. When a `CustomResourceDefinition` is created, updated, or deleted, the manager checks whether its API group appears in `manager/assets/managed_groups`. If it does, a router is started (or stopped) automatically.

**Format:** one API group per line.

```txt
composition.krateo.io
widgets.templates.krateo.io
core.krateo.io
```

Rules:

- One group per line, blank lines are ignored
- Groups are matched **exactly** against the CRD's `spec.group`
- Adding a group here causes the service to begin watching all CRDs installed under that group
- Removing a group and redeploying stops watching those resources
- The CRD kind itself must be listed in the static routers file above for this mechanism to receive events

## How It Works

### 1. Startup Sequence

1. Waits for PostgreSQL to become available
2. Detects the cluster name
3. Starts the health probe server
4. Initializes the Kubernetes dynamic client
5. Launches the batch writer and worker pool
6. Starts static routers for fixed resource kinds
7. Starts the dynamic manager to respond to CRD changes
8. Begins processing events

### Event Flow

1. Kubernetes emits a resource change (add, update, or delete).
2. The router enqueues a `QueueItem` containing the object key and GVR.
3. The worker pool dequeues the item and calls `reconcile`:
   - For CRDs (`apiextensions.k8s.io`): routes to the Manager via event bus to start or stop a dynamic router.
   - For all other resources: looks up the current object from the informer store and routes to Storage.
4. The ingester:
   - Resolves the composition ID from labels
   - Extracts metadata (group, version, kind, plural, cluster name)
   - Sets `deleted_at` for delete events
   - Builds a structured record
5. The record becomes a queued batch job.
6. The batch writer flushes records to PostgreSQL.

## Health Probes

The service exposes probe endpoints suitable for Kubernetes deployments.

| Endpoint | Purpose |
|---|---|
| `/livez` | Process is alive |
| `/readyz` | Database reachable and service ready |

## Metrics

Prometheus metrics are exposed at `:9090/metrics`.

| Metric | Description |
|---|---|
| Worker processed count | Total successfully processed queue items |
| Worker error count | Total failed queue items |
| Worker processing duration | Histogram of per-item processing time |

## Deployment Notes

Recommended for Kubernetes deployment.

Best practices:

- Store DB credentials in Secrets
- Use resource limits
- Monitor queue depth via the pipeline status log line (emitted every 30 seconds)
- Monitor active informer count and goroutine count in logs
- Enable PostgreSQL connection pooling
- Co-locate with the database when possible to reduce latency
- Grant RBAC `watch`, `list`, and `get` permissions for all resource kinds listed in `router/assets/static` and all CRDs in managed groups