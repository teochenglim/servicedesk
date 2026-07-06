# ServiceDesk — Design 06: Technical Architecture

## 6.1 Architecture Overview

```
LB (nginx/HAProxy/k8s Ingress) → Go instances (stateless)
                                     │
                     Shared Database (SQLite | MySQL | PostgreSQL)
                                     │
              Background Workers (goroutine pool per instance):
                webhook.Dispatcher  — polls webhook_deliveries
                workflow.Engine     — polls workflow_tasks
              (DB used as the task queue; ClaimNext runs inside a
               transaction so multiple pods can safely share one table)
                                     │
                        Real-time: SSE (sse.Hub) per instance,
                        scoped to that instance's connected clients
```

## 6.2 Technology Stack (as built)

| Component | Choice | Why |
| :--- | :--- | :--- |
| Backend | Go 1.26 | Single static binary, high concurrency. |
| Web Framework | stdlib `net/http` (Go 1.22+ pattern routing: `"POST /tickets/{id}"`) | No framework dependency for basic routing/middleware. |
| ORM | **GORM** (`gorm.io/gorm`) | Chosen mid-build over hand-rolled `sqlx` so placeholder style, generated-ID retrieval (`RETURNING` vs `LastInsertId`), and identifier quoting are handled once instead of per-repo-method per-dialect. |
| DB Drivers | `glebarez/sqlite` (pure-Go, CGO-free — wraps `modernc.org/sqlite`), `gorm.io/driver/mysql` (`go-sql-driver/mysql`), `gorm.io/driver/postgres` (`pgx`) | All three are real, live-tested backends, not aspirational stubs. |
| Frontend | Static HTML + HTMX + Alpine.js (vendored under `web/static/js`, no CDN/build step) | Interactive without Node.js. |
| Templating | Go `html/template` | Auto-escaping. |
| Markdown | `goldmark` (GFM extensions) | Server-side render; syntax highlighting via `highlight.js` client-side. |
| Auth | JWT (`golang-jwt/jwt/v5`), bcrypt | Stateless. |
| Logging | `log/slog` (`internal/logging`, 5 levels) | Structured, standard library. |
| Metrics | `prometheus/client_golang` | `/metrics`, scraped by Prometheus/k8s. |
| Config | Env vars + optional YAML file (`internal/config`) | `gopkg.in/yaml.v3`; env always wins over the file. |

## 6.3 Database-Agnostic Design: What GORM Buys, and What It Doesn't

GORM's query builder (`.Where().Find()`, `.Create()`, `clause.OnConflict`) automatically:
- Translates `?` placeholders to `$1, $2, ...` for PostgreSQL.
- Populates the model's ID field after `Create()` regardless of whether the dialect supports `LastInsertId()` (SQLite/MySQL) or requires `RETURNING id` (PostgreSQL never supports `LastInsertId` — there's no such concept on the wire protocol).
- Quotes every identifier per-dialect, so a column like `trigger` never collides with a reserved word — though this repo still renames it to `trigger_event` via `gorm:"column:trigger_event"` belt-and-suspenders, since raw SQL fragments elsewhere reference it directly.

**What it does *not* abstract**, so the repo layer branches on `r.db.Dialector.Name()` for exactly these:
- **Full-text search** (`TicketRepo.searchPredicate`): SQLite FTS5 virtual tables (`db.applySQLiteFTS`, created after `AutoMigrate` since GORM can't express `CREATE VIRTUAL TABLE`), MySQL `FULLTEXT` index + `MATCH() AGAINST()` (`db.applyMySQLFTS`, added via raw `ALTER TABLE` since GORM's `class:FULLTEXT` index tag gets emitted literally against every dialect and breaks SQLite/Postgres), PostgreSQL `to_tsvector()`/`plainto_tsquery()` computed at query time (no extra index — add a GIN index if this needs to scale).
- **`next_run_at` columns** (`workflow_tasks`, `webhook_deliveries`): stored as a **Unix-epoch `int64`**, not `time.Time`. A bound `time.Time` parameter gets formatted as RFC3339 (`T` separator) by the driver, which never compares correctly against a space-separated `CURRENT_TIMESTAMP` — so both the write and the "is this due yet" comparison use `time.Now().Unix()` explicitly instead of relying on SQL-side `NOW()`/`CURRENT_TIMESTAMP`.
- **String columns with a unique index need an explicit `size`**: MySQL refuses to index a `TEXT`/`BLOB` column without a key-length prefix (error 1170), and GORM defaults an untagged Go `string` field to `TEXT` on MySQL. Every `uniqueIndex`-tagged string field in `internal/models/models.go` carries an explicit `size:190` (or smaller) for this reason.

## 6.4 Choosing a Database

| Driver | Use when | Notes |
| :--- | :--- | :--- |
| `sqlite` (default) | Demo, single-team self-hosted instance, k8s single-replica | Single writer (`SetMaxOpenConns(1)`), file-based, zero external dependency. **Do not scale to >1 replica.** |
| `mysql` | Existing MySQL infra, need FULLTEXT search | Needs MySQL 8+ (FULLTEXT on InnoDB). |
| `postgres` | Production / horizontal scaling / k8s multi-replica | Default in `k8s/10-configmap.yaml`; supports `to_tsvector` search. Postgres 18+ images expect the data volume mounted at `/var/lib/postgresql` (not `.../data`) — see `docker-compose-postgresql.yml`. |

Select via `SERVICEDESK_DB_DRIVER` (or `db.driver` in a YAML config file) — see [config.example.yaml](../config.example.yaml).

## 6.5 Key Tables

- **`tickets`**: fixed fields + `custom_fields` (JSON), `org_id` (multi-tenant scope).
- **`organizations`** / **`org_memberships`**: multi-tenant boundary (§02.3), self-referencing `parent_id` for a future Group→Company→Department hierarchy.
- **`queues`** / **`queue_memberships`**: hierarchical queues + who can pick up from each.
- **`tags`** / **`ticket_tags`**: label/RCA support.
- **`webhooks`** / **`webhook_deliveries`**: registrations + a durable delivery outbox.
- **`workflows`** / **`workflow_tasks`**: rule/runbook definitions + the async task queue.
- **`approvals`**: per-step approval decisions feeding back into `workflow_tasks`.
- **`event_logs`**: event-sourcing audit trail (immutable).

## 6.6 Deployment

- **Docker**: multi-stage `Dockerfile`, `CGO_ENABLED=0`, distroless `nonroot` runtime image. The data volume's ownership is fixed up at build time (`COPY --chown=65532:65532`) since distroless has no shell to `chown` at runtime.
- **docker-compose**: three variants — `docker-compose.yaml` (sqlite, default), `docker-compose-mysql.yml`, `docker-compose-postgresql.yml` — each includes the DB service with a healthcheck the app `depends_on`.
- **Kubernetes** (`k8s/`): ConfigMap + Secret (example, gitignored real copy) for the app, a PostgreSQL `StatefulSet` for a self-contained cluster, Deployment (2+ replicas, `/health` liveness/readiness probes) + Service + Ingress + HPA (CPU-based, 2–10 replicas).
- **CI** (`.github/workflows/ci.yml`): `go vet` + `go build` + `go test` on every push/PR; build-and-push to GHCR on `main`/tags only.
