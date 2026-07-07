# ServiceDesk — Design 03: Functional Requirements

## 3.1 Ticket Management (Lifecycle)

### 3.1.1 Fields (Fixed + Dynamic)

- **Fixed**: Ticket ID, Title, Description (Markdown), Priority (P1–P4), Status, Queue, Assignee, Creator, Org (tenant), Timestamps, SLA Due Date.
- **Dynamic Custom Fields** (`CustomFieldDef`, admin-configurable): Text, Number, Dropdown, Date, Multi-select. Bound to a Category, rendered dynamically.

### 3.1.2 Explicit State Machine

Implemented in `internal/service/statemachine.go` (`nextStatus`), covered by unit tests in `statemachine_test.go`:

```
New → (Assign/Pickup) → In Progress
In Progress → (Resolve) → Resolved
Resolved → (Confirm) → Closed
Resolved → (Reopen) → In Progress
In Progress → (Reject) → Rejected → Closed (forced, single call)
Closed → (Reopen) → In Progress (SystemAdmin or the original Customer creator only)
```

Every transition logs the actor, timestamp, action, and optional reason to `event_logs` (audit trail / event sourcing). Invalid transitions are rejected by the engine before any write happens.

### 3.1.2b Stage Tracking Overlay (MTTD/MTTA/MTTM/MTTR)

Alongside the operational status above, every ticket also tracks four stage timestamps purely for display and metrics — a Detect → Ack → Mitigate → Resolve view of the same ticket. This is **additive**, not a second state machine: it never gates or replaces a status transition, and `Rejected` sits outside it entirely (a rejected ticket just shows its plain status badge, no progress bar).

- `DetectedAt` — defaults to the ticket's `CreatedAt`; for Agent-created tickets (an automation/monitoring actor filing on a human's behalf) it can be set earlier, to when the underlying trigger fired, so `MTTD = CreatedAt - DetectedAt` is near-zero for a Customer-filed ticket but meaningful for an Agent-filed one.
- `AckedAt` — stamped once, the first time a ticket leaves `New` (first pickup/assign). Never overwritten.
- `MitigatedAt` — stamped by a new lightweight action, `mark_mitigated` (Engineer or Agent, while `In Progress`), that does **not** change `Status` — it only marks that a workaround is in place. Overwritten if mitigated again after a reopen.
- `ResolvedAt` — stamped when the existing `resolve` action fires. Cleared on `reopen` (the ticket is back in flight) and re-stamped if resolved again.
- `ReopenCount` — incremented each time `reopen` fires. On reopen, the progress bar visually resets to whichever of `AckedAt`/`MitigatedAt` is the latest non-nil milestone, tagged "↺ reopened, Nth time".

MTTD/MTTA/MTTM/MTTR are simple deltas between consecutive stage timestamps (Detect→Ack, Ack→Mitigate, Mitigate→Resolve), aggregable per-queue and per-shift straight from these columns — no new event-sourcing system needed, `event_logs` already timestamps every transition and gains one new event kind (`ticket_mitigated`) for the new action. See [08_design_ux.md](08_design_ux.md) §8.2 for the shared Ticket Progress Bar component this data drives.

### 3.1.3 Label System (Incident + RCA)

- **Incident Labels**: free-form tags (`database`, `network`, `password-reset`). Multiple per ticket.
- **RCA Labels**: for Problem Management (`hardware-failure`, `config-error`, `human-error`). A ticket can carry one or several.
- Tags are `(name, kind)` pairs where `kind ∈ {incident, rca}`; Engineers can add/remove them during triage.

### 3.1.4 Watch / Unwatch

- Creating a ticket auto-watches the creator.
- Any user can manually watch a ticket they can see.
- Watchers receive SSE pushes for status changes and new notes.
- **Multi-tenant note**: watching is also how a Customer gains visibility into a same-org ticket they didn't create (§02, "Ticket visibility rules").

## 3.2 Queues & Assignment

See [02_design_roles_and_tenancy.md](02_design_roles_and_tenancy.md) §2.2 for the full pickup/assign/transfer rules. Summary: Engineers pick up (self-assign) from queues they belong to; Manager or SystemAdmin (holds `CapQueueOps` directly, §2.1.1) assign/transfer to anyone.

## 3.3 Notes (Internal vs. External)

- **External Notes**: visible to the Customer.
- **Internal Notes**: Engineers/Manager/SystemAdmin only.
- Both support Markdown (via `goldmark`, GFM extensions) including fenced code blocks; syntax highlighting is applied client-side with highlight.js.
- Adding a note fires an SSE push, a webhook event (`note.added.external` / `note.added.internal`), and the `note_added` workflow trigger.

## 3.4 Problem Management & RCA

- A **Problem** groups tickets sharing a root cause; created/edited by Engineers.
- Each Problem carries `RootCause`, `Resolution`, `PreventiveMeasures`.
- Linking a ticket to a Problem can also attach an RCA label in one step.

## 3.5 Workflow Engine (Lightweight)

- **Triggers**: `ticket_created`, `status_changed`, `field_updated`, `note_added`, or a manual "start runbook" button on a ticket.
- **Step types** (one interpreter handles both plain rule workflows and Runbooks — see 04): `condition`, `user_input`, `http_request`, `template_render`, `add_note`, `auto_assign`, `webhook`, `approval`, `notify`.
- **Approvals**: a step can require a named role (e.g. `Manager`) to approve/reject before the workflow continues; rejection stops the workflow rather than continuing.
- **Implementation**: `workflow.Engine` + a background worker pool (goroutines), polling a `workflow_tasks` table used as a durable queue (`ClaimNext` claims the oldest due row inside a transaction).
- Definitions are stored as JSON in `workflows.config` and edited via `/admin/workflows`.

## 3.6 Webhooks & Integrations

- **Trigger Events**: ticket created, status changed, field updated, note added (internal/external), and any workflow `webhook` step.
- **Configuration**: `/admin/webhooks` registers a URL + comma-separated event list (or `*`) + an HMAC secret.
- **Payload**: JSON `{event, payload, timestamp}`; signed with `X-ServiceDesk-Signature: hex(hmac_sha256(secret, body))`.
- **Delivery**: a durable outbox (`webhook_deliveries`) with `webhook.Dispatcher`, exponential backoff (2s/4s/8s), max 3 attempts before marking `failed`.

## 3.7 Search & Listing

- **Full-Text Search** across Title, Description, and Notes — implemented per database dialect (see [06_design_technical_architecture.md](06_design_technical_architecture.md) §6.4): SQLite FTS5, MySQL `FULLTEXT` + `MATCH...AGAINST`, PostgreSQL `to_tsvector`/`plainto_tsquery`.
- **Advanced Filters**: Status, Priority, Queue, Category, Label, Date Range, Assignee, Watched status.
- **Views**: My Tickets (created), My Queues (Engineer pickup pool), Assigned to Me, Watched Tickets, All Tickets (staff).

## 3.8 Real-time Updates (SSE)

- `sse.Hub` fans out ticket events to the watchers + assignee of that ticket over `GET /events` (`EventSource`).
- Frontend JS (`web/static/js/app.js`) listens and triggers an HTMX refresh of the ticket panel when an event matches the ticket currently open.

## 3.9 Authentication

- **Default**: DB authentication (bcrypt). A first-run `admin/admin123` account is created automatically if no static users are configured (with a startup warning to change it).
- **Static Users**: `SERVICEDESK_STATIC_USERS="alice:pass:SystemAdmin,bob:pass:Engineer"` for demos/testing.
- **Multi-tenant login**: see §02.3. LDAP/OAuth2 remain documented extension points, not implemented (`LDAP_ENABLED` is parsed but not wired to a real directory yet).
