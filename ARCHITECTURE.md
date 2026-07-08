# Architecture

How the code is organized and how a request flows through it. For *what* the system does and *why*, see [DESIGN.md](DESIGN.md); this file is about *how* it's built.

## Layering

```
cmd/servicedesk/main.go   — wires everything together, owns the process lifecycle
        │
internal/httpapi          — HTTP handlers + router (net/http, Go 1.22+ pattern routing)
        │
internal/service           — business logic: ticket state machine, notes, problems
        │
internal/repo               — one file per entity, thin GORM wrappers
        │
internal/models              — GORM-tagged structs (the schema, in Go)
        │
internal/db                   — dialect selection + AutoMigrate + per-dialect extras
```

Handlers never touch `internal/repo` directly for ticket mutations — they go through `internal/service`, which owns the state machine, RBAC/queue-membership checks, audit logging, and fan-out (SSE/webhooks/workflow triggers). Handlers *do* call simple repos directly for read-mostly, no-business-logic entities (queues, users, tags, webhooks, workflows) rather than wrapping every CRUD operation in a pass-through service.

## Request flow (example: pickup a ticket)

1. `POST /tickets/{id}/pickup` hits `internal/httpapi/router.go`, which wraps the handler in `middleware.RequireAuth` (parses the JWT cookie/header into `auth.Claims` in the request context) and `middleware.RequireRole(Engineer)`.
2. `ticket_handlers.go:handleTicketPickup` reads the ticket ID from the path, pulls `claims` from context, calls `service.TicketService.Pickup`.
3. `TicketService.assign` (shared by Pickup/Assign) checks queue membership for Engineers (Manager or SystemAdmin bypass it via `Role.Can(CapQueueOps)`, which SystemAdmin holds directly — see [DESIGN/02](DESIGN/02_design_roles_and_tenancy.md) §2.1.1), runs the state-machine transition (`service/statemachine.go`), persists via `repo.TicketRepo.Update`, writes an audit `event_logs` row, then fans out: `sse.Hub.Publish` (real-time), `webhook.Dispatcher.Dispatch` (outbox), `workflow.Engine.Trigger` (enqueue any subscribed workflow).
4. The handler redirects to `/tickets/{id}`; the ticket detail template re-renders with the new state.

Everything downstream of "fan out" is decoupled behind three small interfaces defined in `internal/service/interfaces.go` (`EventPublisher`, `WebhookDispatcher`, `WorkflowTrigger`) so `internal/service` doesn't import `internal/sse`, `internal/webhook`, or `internal/workflow` directly — those packages depend on `service`'s interfaces, not the other way around.

`sse.Hub.Handler` (`GET /events`) needs the response writer it's given to satisfy `http.Flusher` to stream at all — every request passes through `metrics.Middleware` first (wraps the whole mux, see `router.go`'s `Routes()`), so that wrapper (`statusRecorder`) must implement its own `Flush()` that delegates to the real writer (RELEASE/v_3.0.7.md). Embedding `http.ResponseWriter` in a wrapper struct only promotes that interface's own methods — `Flush` belongs to the separate `http.Flusher` interface and is never promoted, so any *new* response-wrapping middleware added later needs the same explicit `Flush()` delegation or `/events` silently breaks again (500 "streaming unsupported" on every connection, with `EventSource` swallowing the error via its own silent auto-reconnect — nothing surfaces to a user, only to the browser console/network tab).

## Background workers

Two independent poll loops run per process (`cmd/servicedesk/main.go` spawns `cfg.WorkerPoolSize` of each):

- `workflow.Engine.Run` — claims due rows from `workflow_tasks` (`ClaimNext`, transactional), executes steps (`execute`/`runStep`), possibly pausing at `user_input`/`approval` steps.
- `webhook.Dispatcher.Run` — claims due rows from `webhook_deliveries`, POSTs the signed payload, retries with backoff.

Both `ProcessOne()` methods are exported specifically so integration tests can call them synchronously instead of racing a timer (`internal/httpapi/integration_test.go` does this for the Runbook and webhook tests).

## Multi-database support

`internal/db/db.go` picks a GORM dialector (`sqlite`/`mysql`/`postgres`) and runs `AutoMigrate` against the structs in `internal/models`. GORM handles placeholder style and generated-ID retrieval uniformly; the repo layer only branches by dialect for the handful of things GORM can't express — see [DESIGN/06](DESIGN/06_design_technical_architecture.md) §6.3 for the specifics (full-text search, `next_run_at` as Unix epoch, MySQL index-length on `TEXT` columns).

## Frontend

Static HTML templates (`web/templates/*.html`, embedded via `go:embed` in `web/embed.go`) rendered server-side with Go's `html/template`, enhanced with HTMX for partial-page interactions and Alpine.js for small client-side state. No Node/build step — HTMX/Alpine/highlight.js/Toast UI Editor are vendored directly under `web/static/js`.

When vendoring a library like this, the file fetched matters: `@toast-ui/editor`'s own npm `dist/toastui-editor.min.js` (what jsDelivr's npm mirror serves) is a bundler-target build that `require()`s ProseMirror as peer dependencies — it throws immediately (`toastui.Editor is not a constructor`) when loaded as a bare `<script>` tag with no bundler to resolve those imports, and this reproduces identically across every browser engine, so it's easy to mistake for something else being broken (RELEASE/v_3.0.6.md — this exact mistake shipped and went unnoticed for a while). The correct artifact for drop-in `<script>`-tag use is the self-contained `-all` bundle from the project's own CDN (`uicdn.toast.com/editor/latest/toastui-editor-all.min.js`), which bundles ProseMirror internally. The same "does this need a bundler to resolve peer deps, or is it truly self-contained" question is worth checking before vendoring any new no-build-step dependency.

## Testing strategy

- `internal/service/statemachine_test.go` — pure unit tests of the state machine's transition table (white-box, in-package).
- `internal/httpapi/integration_test.go` + `testserver_test.go`/`client_test.go` — full-stack integration tests: wire the exact same dependency graph as `main.go` against an in-memory SQLite DB, serve it via `httptest.Server`, and drive it with a cookie-jar `http.Client` the way a browser would. This is the primary safety net for RBAC, multi-tenancy, and the workflow engine — several real bugs (MySQL index length, Postgres 18 volume path, the approval-resume `StepIndex` bug) were caught this way, not by code review.
- None of the above executes client-side JS — a `curl`/`http.Client`-based check only ever sees the server-rendered HTML, so a bug purely in `app.js` or a vendored library (the Toast UI Editor mounting failure, the dead SSE live-update stream) is invisible to it: the HTML looks correct, the mount `<div>` is right there in the markup, nothing in Go-side testing would ever fail. Catching that class of bug needs an actual browser executing the page's JS — see `CLAUDE.md`'s note on using Playwright for this.

## Adding a new entity

1. Add a GORM-tagged struct to `internal/models/models.go`.
2. Register it in `internal/db/db.go`'s `AutoMigrate` call.
3. Add a `internal/repo/<entity>.go` with a `*gorm.DB`-backed repo (see any existing one for the shape).
4. If it needs business logic (state, RBAC, fan-out), add a `internal/service/<entity>.go`; otherwise wire the repo straight into `internal/httpapi`.
5. Add handlers + routes in `internal/httpapi`, templates in `web/templates`.
6. Wire construction in `cmd/servicedesk/main.go` (and `internal/httpapi/testserver_test.go` if it needs test coverage).
