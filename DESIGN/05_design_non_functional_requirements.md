# ServiceDesk — Design 05: Non-Functional Requirements

- **Performance**: target 500+ concurrent users per instance; list queries < 200ms; detail load < 100ms.
- **Availability**: stateless app tier — horizontal scaling is safe against MySQL/PostgreSQL (multiple replicas). SQLite is single-writer and single-instance only; use it for small/self-hosted deployments, not HA (see [06_design_technical_architecture.md](06_design_technical_architecture.md) §6.4 and the k8s manifests, which default to PostgreSQL for exactly this reason).
- **Security**:
  - JWT authentication (HS256), claims include `{uid, username, role, org_id}`.
  - RBAC via `Role.AtLeast` rank checks in `internal/middleware` + per-route gating in `internal/httpapi/router.go`.
  - Multi-tenant isolation enforced at the query layer (`repo.CustomerScope`), not just the UI.
  - bcrypt password hashing; webhook payloads HMAC-SHA256 signed.
  - Input rendering uses Go's `html/template` (auto-escaping) and `goldmark` for Markdown — no raw HTML injection path.
  - Full audit logging via `event_logs` (event sourcing: every ticket mutation records actor, timestamp, and a JSON detail blob).
- **Observability**:
  - Structured logging (`log/slog`) with five levels — `fatal`, `error`, `warning`/`warn`, `info`, `debug` — configured via `SERVICEDESK_LOG_LEVEL` (`internal/logging`). `fatal` always logs regardless of the configured minimum, then exits.
  - Prometheus metrics at `/metrics` (`internal/metrics`): HTTP request counts/latency by route, ticket transitions, notes created, webhook delivery outcomes, workflow task outcomes, SSE client gauge.
  - `/health` readiness endpoint pings the underlying `sql.DB`.
- **Extensibility**:
  - Pluggable Service layer with hooks (webhooks, workflow triggers, SSE) behind small interfaces (`service.EventPublisher`, `service.WebhookDispatcher`, `service.WorkflowTrigger`) so the ticket service doesn't import the SSE/webhook/workflow packages directly.
  - Database-agnostic via GORM: SQLite (default, pure-Go via `glebarez/sqlite`/`modernc.org/sqlite`), MySQL, and PostgreSQL are all real, tested backends — not just SQLite with aspirational driver stubs.
  - Configuration via environment variables **or** a YAML file (`-config path.yaml` / `SERVICEDESK_CONFIG_FILE`), with env vars always taking precedence over the file so a mounted k8s ConfigMap can still be overridden ad hoc.
- **Maintainability**: layered architecture (Handler → Service → Repository → GORM), `go vet`/`gofmt` clean, an integration test suite (`internal/httpapi/integration_test.go`) that spins up the real HTTP server against an in-memory SQLite DB and exercises it exactly like a browser/curl would (cookie-jar client), plus state-machine unit tests.
