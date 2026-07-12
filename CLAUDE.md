# CLAUDE.md

Orientation for working in this repo. Read this first; it points to everything else rather than duplicating it.

## What this is

ServiceDesk: a single-binary Go ITSM ticketing system (multi-tenant, queue-based routing, workflow/Runbook automation, webhooks) with a static HTML/HTMX frontend. Full context, in order:

1. **[DESIGN.md](DESIGN.md)** → [DESIGN/](DESIGN/) — *what* the system does and why (roles, tenancy, functional/non-functional requirements, technical architecture). Read this before changing behavior.
2. **[ARCHITECTURE.md](ARCHITECTURE.md)** — *how* the code is organized, request flow, layering rules, "adding a new entity" checklist. Read this before adding code.
3. **[RELEASE.md](RELEASE.md)** → [RELEASE/](RELEASE/) — what's actually shipped vs. planned per version, plus real bugs found and how they were fixed. Check the relevant `v_x.y.z.md` before assuming a feature works a certain way.
4. **[README.md](README.md)** — how to run it (local, Docker, k8s) and configuration reference.
5. **[DEMO.md](DEMO.md)** — demo-mode account list and a `curl`-only smoke test (`make demo-curl-test`) for verifying a running instance without a browser.

Licensed [Apache 2.0](LICENSE). Current version lives in [VERSION](VERSION), not hardcoded anywhere else.

## Where things go

| Kind of change | Goes in |
| :--- | :--- |
| Domain struct / DB schema | `internal/models/models.go` (GORM tags), then register in `internal/db/db.go`'s `AutoMigrate` |
| Data access (CRUD, queries) | `internal/repo/<entity>.go` — thin `*gorm.DB` wrapper, no business logic |
| Business logic, state machine, RBAC | `internal/service/<entity>.go` |
| HTTP handlers + routes | `internal/httpapi/<entity>_handlers.go` + `router.go` |
| Page templates | `web/templates/*.html` (embedded via `web/embed.go`) |
| CSS/JS (vendored, no build step) | `web/static/{css,js}` |
| Background workers | `internal/workflow` (workflow/runbook engine), `internal/webhook` (delivery outbox) |
| Config keys | `internal/config/config.go` (env var + YAML field, both), *and* the Configuration table in [README.md](README.md) — that table is the user-facing reference, config.go alone isn't enough |
| New DB dialect quirk | branch on `r.db.Dialector.Name()` in the relevant repo file — see [DESIGN/06](DESIGN/06_design_technical_architecture.md) §6.3 for the existing branch points before adding another |
| Tests | unit: alongside the package (`_test.go`, white-box); full-stack: `internal/httpapi/integration_test.go` using the `testEnv`/`client` helpers in that package |

Full layering rules and the request-flow example are in [ARCHITECTURE.md](ARCHITECTURE.md) — don't put business logic in handlers, don't have `internal/service` import `internal/sse`/`internal/webhook`/`internal/workflow` directly (use the interfaces in `internal/service/interfaces.go`).

## Build, test, run

`make` with no arguments prints the full menu. Key targets:

```
build          Build the servicedesk binary into ./bin
run            Run the server locally (sqlite, ./servicedesk.db)
demo           Run locally in demo mode (sqlite, seeds demo data on first boot - see DEMO.md)
test           Run the full test suite
test-verbose   Run the full test suite with verbose per-test output
vet            Run go vet
fmt            Format all Go source with gofmt
tidy           Tidy go.mod/go.sum
clean          Remove local build artifacts and the dev sqlite DB
docker-build   Build the servicedesk Docker image
up / up-d      Start the sqlite stack (foreground / background)
down           Stop the sqlite stack and remove its volume
up-mysql / down-mysql        Start/stop the MySQL-backed stack
up-postgres / down-postgres  Start/stop the PostgreSQL-backed stack
k8s-apply / k8s-delete       Apply/delete the k8s/ manifests
k8s-logs       Tail logs from the servicedesk deployment in k8s
version        Print the version currently in VERSION
tag            Create and push a git tag for the current VERSION
release        Bump, commit, tag, push - triggers GitHub Actions (VERSION=x.y.z required)
```

**Before claiming a change works**: run `make vet test`, and if it touches DB code, smoke-test against at least sqlite (`make up`) — MySQL/Postgres bugs in this project have consistently been dialect-specific and invisible to `go vet`/unit tests (see [RELEASE/v_1.0.0.md](RELEASE/v_1.0.0.md) "Bugs found and fixed").

**If a change touches `web/static/js` or a vendored frontend library, `go test`/`curl` smoke-testing it is not enough** — neither one executes client-side JS, so a bug purely in `app.js` or a vendored bundle (a markdown editor that never actually mounts, an SSE stream that silently never delivers) looks completely fine from the server side: the HTML is correct, the mount point is right there in the markup, nothing server-side ever fails. Two bugs exactly like this shipped unnoticed until a live user hit them (RELEASE/v_3.0.6.md, RELEASE/v_3.0.7.md). Verify with an actual browser executing the page: `make demo` in one terminal, then drive it headlessly with Playwright's Python bindings (already available on this machine as the system Python's `playwright` package, not an npm one — invoke via `/opt/homebrew/opt/python@3.12/bin/python3.12`, `sync_playwright()`, `page.on("pageerror", ...)` to catch JS exceptions the rendered HTML would never reveal).

**Before ending a task**: if the change affects behavior, update the relevant doc(s) — [DESIGN.md](DESIGN.md)/[DESIGN/](DESIGN/) if it changes *what*/*why* the system does something, [ARCHITECTURE.md](ARCHITECTURE.md) if it changes *how* the code is organized or adds a durable gotcha future work should know about, and always [RELEASE.md](RELEASE.md) (newest entry first) + a new `RELEASE/v_x.y.z.md` for what shipped. Don't wait to be asked for each one separately. Then close out with a 1-line git commit message summarizing the change (the user commits it themselves — don't run `git commit` or `git bump` unless explicitly asked).

## CI/CD

- **`.github/workflows/ci.yml`** — every push/PR runs `go vet` + `go build` + `go test`. On push to `main` or a `v*.*.*` tag, it additionally builds and pushes the Docker image to `ghcr.io/<repo>` (skipped for PRs).
- **`.github/workflows/security.yml`** — Semgrep (SAST: Go, SQL injection, secrets, OWASP Top 10) + Trivy (Go module CVEs + built container image), on every push/PR to `main` and weekly. Fails on CRITICAL/HIGH findings; also uploads SARIF to the repo's Security tab.
- **`.github/workflows/release.yml`** — triggered by a `v*` tag push (i.e. `make release VERSION=x.y.z`): gates on tests, builds a cross-platform binary matrix (linux/darwin/windows × amd64/arm64), and creates a GitHub Release with the archives attached.

Do not run `make release` or any git push/commit/tag command yourself unless the user explicitly asks — releasing is the user's call.

## Conventions worth knowing before you're surprised by them

- **`next_run_at` columns are Unix-epoch `int64`, not `time.Time`** — see [DESIGN/06](DESIGN/06_design_technical_architecture.md) §6.3. Don't "fix" this back to `time.Time`.
- **String fields with `uniqueIndex` need an explicit `size:` tag** (MySQL can't index unsized `TEXT`).
- **Queue membership vs. role**: "Tier 1/2/3" is not a role — it's which `Queue` an `Engineer` belongs to. Don't reintroduce tiered roles; use queue membership.
- **Org (`OrgID`) scoping only applies to Customers.** Engineer/Manager/SystemAdmin are intentionally unscoped ("all for all") — don't add org filtering to staff-facing queries.
- **Capabilities are earned per-role, not by rank — except `SystemAdmin`, which holds every capability unconditionally.** `Manager` holds `CapQueueOps` as its native capability; `SystemAdmin` holds it too, but via `Role.Can`'s explicit `SystemAdmin` override, not by outranking `Manager` through `Role.AtLeast` — see [DESIGN/02](DESIGN/02_design_roles_and_tenancy.md) §2.1.1. Don't gate new capability-style actions with `AtLeast`; use `Role.Can(models.Capability)`, and don't add `SystemAdmin` to a capability's role list by hand — it already passes every `Can` check.
- **`Engine.Resume` must not increment `StepIndex`.** Each step type's `runStep` case re-checks its own resume marker in the context and decides whether to advance — this is how a rejected `approval` step stops the workflow instead of silently continuing. See [DESIGN/04](DESIGN/04_design_runbook_hook.md) §4.3 before touching `internal/workflow/engine.go`.
