# ServiceDesk — Design 08: Persona-Driven UX

Core UX principle applied throughout: put the button a role uses 80% of the time at the top, in one tap. Push everything else into a secondary disclosure. Each role gets a different home screen, a different primary action, and a different default sort — all reading from the same ticket/queue data described in [02](02_design_roles_and_tenancy.md)/[03](03_design_functional_requirements.md).

## 8.1 The five personas

| Persona | Job to be done | Primary action (1 tap) | Secondary (progressive disclosure) |
| :--- | :--- | :--- | :--- |
| ServiceDeskAdmin | Own the system itself: people, permissions, integrations | Sudo as any user | User management, org-wide settings, audit log |
| Customer (User) | Get my issue fixed, know where it stands | Submit ticket, see status | Close, Reopen, add note — always available, any state |
| Engineer | Move tickets forward, hand off, or pick up more work | Change status, Transfer, Pick up | Add note (internal/external), tag, raise ticket for a user |
| Service Desk Manager | Own the queues; run the shift; catch risk before an SLA breach; rebalance load | Transfer ticket to engineer or queue; open the dashboard | Create/edit queues, drill into a queue/engineer/ticket |
| Agent (automation) | Detect fast, ack fast, mitigate if it can, escalate if it can't | Create ticket, Ack, post mitigation note | Attempt auto-resolve; otherwise Transfer to a human queue |

The Agent persona is a non-human actor — an automation/monitoring system that can detect issues, file tickets, acknowledge, attempt mitigation, and hand off to a human when it can't finish the job. It uses the same API and state machine as everyone else; it does not get a special back door. Same ticket record, five different "first thing I see" — one of them isn't a screen at all, it's an API contract.

## 8.2 Shared component: Ticket Progress Bar

Rendered per-state from the stage timestamps described in [03](03_design_functional_requirements.md) §3.1.2b (Detect → Ack → Mitigate → Resolve, with Closed as a gate after Resolve and Reopen as a jump back into the flow):

```
Detect ●───────○───────○───────○  Resolve
        2h 14m  (waiting for ack)

Detect ○───────●───────○───────○  Resolve
                 38m since ack · mitigation target: 1h

Detect ○───────○───────●───────○  Resolve
                          Mitigated 20m ago · working on root cause

Detect ○───────○───────○───────●  Resolve → Closed ✓
                                   Resolved 18m ago
```

If a ticket is Reopened, the bar visually resets to the Ack (or Mitigate, if a fix had already landed) dot, with a small "↺ reopened, 2nd time" tag — this keeps the history honest without hiding that it's not the ticket's first pass.

Design rules for the dot-to-dot:

- Filled dot = current stage. Hollow = not yet reached. Red-ringed dot = that stage blew its SLA target.
- The label under the bar always answers two things: how long has it been in this stage, and what happens next.
- Color: sage (`#6A9E77`) on track, coral (`#F46277`) when a stage has blown its SLA target, mid-teal (`#48A2AB`) for the active dot.
- Agent-driven transitions (auto-detected, auto-acked, auto-mitigated) get a small "⚙ via Agent" tag next to the timestamp, so a human glancing at the bar can immediately tell how much of the lifecycle so far was automated versus human-handled.
- `Rejected` tickets show only the plain status badge, no progress bar — that lifecycle never entered Ack/Mitigate (see [03](03_design_functional_requirements.md) §3.1.2b).
- The plain status badge (`New`/`In Progress`/`Resolved`/`Closed`/`Rejected`) and this bar are shown **together**, not one replacing the other — the bar is a display/metrics overlay, not a new status field.

This is the single visual element reused across all five personas' views (list row, ticket detail header, manager dashboard tile). Consistency here is what makes the redesign feel coherent rather than five separate apps.

## 8.3 Persona 1 — ServiceDeskAdmin

Mental model: "I own the system, not the shift." This is a deliberate split from the old model: ServiceDeskAdmin no longer owns queues, assignment, or transfer as native day-to-day actions — that's Service Desk Manager's job (§8.6, [02](02_design_roles_and_tenancy.md) §2.1.1). ServiceDeskAdmin owns the things that keep the system trustworthy and running: who has access, what they can do, and how the org is configured. This keeps the two roles from competing for the same screen space, and means ServiceDeskAdmin's home screen is short, not a superset of everyone else's.

Full control surface (nothing is out of reach, only reordered by frequency):

- Create, edit, deactivate users; set/change user roles.
- Sudo as any user — see [02](02_design_roles_and_tenancy.md) §2.5 for the full mechanics (real actions, audit-logged under the target's identity, persistent banner, explicit return only). This is also how ServiceDeskAdmin reaches queue/ticket actions when genuinely needed (e.g. covering for an absent Manager) — through sudo, not duplicate native buttons.
- Org-wide settings: SSO/auth config, webhook config, Runbook Hook config, global defaults.
- System audit log — every sudo session, every role change, every user deactivation, searchable and exportable.
- Security-relevant read access across the whole system (not operational routing — that's the Manager's dashboard).

Home screen layout:

```
┌───────────────────────────────────────────────┐
│  System administration                         │
├───────────────────────────────────────────────┤
│  Users (42)                    [+ Add user]     │
│  Alice — Engineer          [Edit ▾][Sudo as ▾]  │
│  Bob   — Manager           [Edit ▾][Sudo as ▾]  │
│  Carla — Engineer          [Edit ▾][Sudo as ▾]  │
├───────────────────────────────────────────────┤
│  Recent audit events                            │
│  14:02  Admin sudo'd as Alice → transferred #1039│
│  13:40  Bob role changed: Engineer → Manager     │
├───────────────────────────────────────────────┤
│  [⚙ Org settings]  [🔒 Integrations]  [📋 Audit] │
└───────────────────────────────────────────────┘
```

Redesign decisions:

- This screen is intentionally short. No queue list, no ticket list, no SLA widgets — those belong to Manager and Engineer. ServiceDeskAdmin opening this screen should see people and system health, not operational noise.
- "Sudo as" sits on every user row, one tap away, since it's the one power action ServiceDeskAdmin actually reaches for often — usually to cover for someone or debug a permissions issue, not to route tickets.
- Every sudo session shows the acting-as banner the entire time, and the audit trail records real actions taken under sudo against the target user's identity, not the admin's — that's what makes it accountable rather than just a bypass.
- The audit log is promoted to the home screen, not buried in a settings tab — for a role whose whole job is trustworthy system operation, "what changed recently" is itself a primary-action-adjacent thing to see immediately.
- Org settings, integrations, and the full audit view sit behind clearly labeled buttons at the bottom — still one tap, just below the two things ServiceDeskAdmin does most (manage a user, check what happened recently).

## 8.4 Persona 2 — Customer (User)

Mental model: "I filed something. Is anyone looking at it?" This persona wants reassurance and closure, not a ticketing system.

Home screen layout:

```
┌─────────────────────────────────────┐
│         [+ Submit a ticket]          │  ← biggest button on the page
├─────────────────────────────────────┤
│ My tickets                           │
│ #1042 Payment gateway timeout        │
│ Detect ●───○───○───○  Submitted 2h ago│
│         [View] [Add note] [Close]    │
├─────────────────────────────────────┤
│ #1030 Login issue                    │
│ Resolve ○───○───○───●                │
│ "Fixed the SSO redirect" — 1h ago    │
│  📎 2 attachments                     │
│      [Close ticket] [Reopen] [+Note] │
└─────────────────────────────────────┘
```

Redesign decisions:

- Submit is the loudest element on the screen, full-width, top of page. Everything else is secondary to a Customer.
- The progress bar for this persona drops technical language: no "Ack/Mitigate" jargon overload, just plain-English state + a one-line note quoting (paraphrased) the engineer's last external note, if any.
- Close, Reopen, and Add note are always visible on every ticket, in any state — never gated behind "only when Resolved." A Customer should be able to close a ticket early ("actually it's fine now"), reopen a Closed one that broke again, or add a note at any time without hunting for when the button decides to appear. Buttons that don't make sense yet (e.g. Reopen on a ticket that's still open) are simply not relevant that state, but they're never disabled-and-hidden as a trap — the state machine ([03](03_design_functional_requirements.md) §3.1.2) still legitimately rejects an illegal transition server-side.
- The problem description field (on submission) and every note support rich text: Markdown with a raw/rendered toggle, including fenced code blocks (§8.7). A Customer pasting an error message or a config snippet gets it rendered as code, not mangled into a single paragraph — this starts at the point of submission, not just in Engineer-side tooling.
- Submitting a ticket, or adding a note, also supports file attachments (§8.7) — screenshots, logs, documents. Attachments show as thumbnails inline, associated with the ticket, and are visible to Engineer/Manager/ServiceDeskAdmin too.
- Internal notes are never shown here, obviously — the Customer view only ever renders external notes and external attachments.
- No tagging, no queue names, no engineer names unless the org wants that transparency — Customers shouldn't need to know your internal routing to trust the process.

## 8.5 Persona 3 — Engineer

Mental model: "What's mine, what's urgent, what can I hand off — and what else could I grab." Engineer touches many tickets per shift, so every extra click compounds.

Home screen layout:

```
┌───────────────────────────────────────────┐
│ [My tickets (6)]  [Available in my queues (9)]│  ← two tabs, not one merged list
├───────────────────────────────────────────┤
│ My tickets — everything assigned to me,     │
│ across every queue I belong to              │
│                                             │
│ #1039  VPN cert expired            ⚠ 20m   │
│ [Mitigate ▾]  [Transfer ▾]                 │  ← top row, big tap targets
│ Ack ○──●──○──○                             │
│ + Add note (internal | external)  · tags   │  ← same row, just smaller/lighter
│ 📎 Add attachment                           │
│ + AI summary (collapsed)                    │  ← closed by default, one tap opens
├───────────────────────────────────────────┤
│ #1043  branches from #1038 (split)          │
│ Detect ●───○───○───○   [Mitigate ▾]         │
└───────────────────────────────────────────┘

  [+ Raise ticket for a user]                  ← always visible, own row
```

Available in my queues tab:

```
┌───────────────────────────────────────────┐
│ #1050  Printer offline        Network queue │
│ Detect ●───○───○───○   10m       [Pick up]  │
│ #1051  Wifi dropout           Network queue │
│ Detect ●───○───○───○   4m        [Pick up]  │
└───────────────────────────────────────────┘
```

Redesign decisions:

- "My tickets" and "Available in my queues" are two distinct tabs, not one merged list. "What's mine" and "what could I take on" are different questions with different urgency, and merging them was making the primary list noisy.
- My tickets is scoped to Engineer, not to a single queue — Engineer usually belongs to more than one queue (e.g. Network and Payments), and this view shows everything assigned to them regardless of which queue it's in. This is deliberately different from Manager's queue-level view (§8.6), which is scoped by queue, not by person.
- Pick up is a single-tap self-assign button in the Available tab — an Engineer can grab an unassigned ticket in any queue they belong to without waiting for a Manager to route it. Once picked up, it moves into "My tickets" and behaves exactly like any assigned ticket.
- Status change is a single dropdown that only shows legal next states (respecting the state machine in [03](03_design_functional_requirements.md) §3.1.2) — no free-text status field, no chance of an illegal transition.
- Transfer opens a lightweight queue-or-engineer picker, not a full reassignment form — Engineer's job here is "I can't handle this," not administration.
- "Raise ticket for a user" sits as its own always-visible row, not buried in a menu — this covers phone-in issues, walk-ups, or anything a customer reports verbally. Engineer fills the same submission form as Customer (§8.4), attributed as "raised by {Engineer} on behalf of {Customer}," and it behaves identically to a Customer-filed ticket from that point on (same progress bar, same Close/Reopen rights for the Customer).
- Suggest split appears on the AI summary panel (§8.9) whenever detected agenda items look unrelated — tapping it uses the same "raise ticket for a user" mechanism to spin off a new linked ticket per item, without asking the Customer to refile anything themselves.
- Internal vs external note is two distinct buttons, not a dropdown or checkbox — this is the one place mis-clicks are costly (an internal note leaking to a customer), so make the choice visually explicit rather than a toggle state that can be missed.
- The note editor and problem-description field support Markdown, with a raw/rendered toggle, including fenced code blocks (§8.7) — pasting a stack trace or a config diff should not come out as a wall of unformatted text.
- Attachments (§8.7) attach directly to the ticket or to a specific note, shown as thumbnails, visible to whoever that note's visibility (internal/external) permits.
- The list defaults to age-within-current-stage, not creation date — a ticket that's been stuck at "Ack" for 3 hours is more urgent than one that just moved to "Mitigate."

## 8.6 Persona 4 — Service Desk Manager

Mental model: "I own the queues and the shift. Who needs help right now, and are we going to miss an SLA." This role owns queue structure and routing day-to-day, not just monitoring — it's the operational owner of "how work flows," while ServiceDeskAdmin (§8.3) owns the system underneath it (people, permissions, integrations). See [02](02_design_roles_and_tenancy.md) §2.1.1/§2.5 for the full capability model: Manager holds queue-ownership natively, ServiceDeskAdmin reaches it only through Sudo-as.

Queue management (Manager's native scope): create/edit/archive queues, set/adjust per-queue SLA targets, assign/reassign tickets across queues (the same action Engineer's "Transfer" triggers, just with org-wide reach instead of "hand off what's mine").

Home screen layout:

```
┌────────────────────────────────────────────────────┐
│  Shift overview — 14:00–22:00      [Activity list ⇄]│
│  [+ New queue]  [Queue settings ▾]                   │
├────────────────────────────────────────────────────┤
│  Queue: Payments        Queue: Network               │
│  8 open · 2 breaching   5 open · 0 breaching          │
│  ▓▓▓▓▓▓░░ 75% healthy   ▓▓▓▓▓▓▓▓ 100% healthy         │
├────────────────────────────────────────────────────┤
│  Engineer load                                       │
│  Alice   ●●●●●●● (7)   ⚠ overloaded  [Transfer ▾]     │
│  Bob     ●●● (3)                     [Transfer ▾]     │
│  Carla   ●● (2)        idle capacity — reassign here? │
├────────────────────────────────────────────────────┤
│  MTTA 12m · MTTM 28m · MTTR 3h 40m · MTTD 4m          │
│  [chart: MTTR trend, last 7 shifts]                   │
└────────────────────────────────────────────────────┘
```

Activity list view (toggle from dashboard) — a second view, one tap away, built specifically to answer "what's the latest on everything" without opening each ticket:

```
┌──────────────────────────────────────────────────┐
│ #1039  VPN cert expired          Ack ○──●──○──○   │
│ Alice · Network · 20m in stage                    │
│ "Confirmed cert expired on edge-gw-3. Checking    │
│  if the renewal script silently failed. Will      │
│  escalate to Bob if not resolved in 30m."          │
│ — Alice, 4m ago                        [Transfer ▾]│
├──────────────────────────────────────────────────┤
│ #1042  Payment gateway timeout   Detect ●──○──○──○│
│ Unassigned · Payments · 2h 14m                    │
│ "Customer reports intermittent 504s on checkout.  │
│  No engineer assigned yet."                        │
│ — Customer, 2h 14m ago                 [Transfer ▾]│
└──────────────────────────────────────────────────┘
```

Design rules for this view:

- Each row shows the last message only, truncated to 3 lines, not the full thread — this view exists so a Manager can scan "what changed since I last looked" across every open ticket in one screen, not read full history.
- Sort by most-recently-updated first, so a shift handover is literally "scroll from the top until you hit stuff you've already seen."
- The last message is whichever came latest across notes and status changes — an engineer's note, a customer reply, or an automated "moved to Mitigate" event all count.
- Transfer sits on every row — to a different engineer or a different queue — so a Manager can rebalance directly from this scan view without drilling into the ticket first.

Redesign decisions:

- Dashboard-first, ticket-detail-last, activity-list for a fast scan. Manager rarely needs a single ticket's full history; they need aggregate signal or "what's new." Clicking a queue or engineer drills into detail; the activity list is the middle ground between the two.
- Load is shown as a simple dot count per engineer, not a table — at-a-glance overload detection ("Alice has 7, Carla has 2") is the whole point of this screen. Flag overloaded engineers automatically (e.g. load > team average + 1 SD, or a configurable threshold).
- Transfer is a Manager primary action, on both the dashboard and the activity list — reassign a ticket to a different engineer or move it to a different queue directly, without needing to ask ServiceDeskAdmin to do it.
- SLA risk is queue-level and shift-level, aggregated, with drill-down to the individual breaching tickets — Manager's question is "which queue is on fire," not "what does ticket #1039 say," except when scanning the activity list for the latest word from the field.
- Core metrics, using the same stage-timestamp data model from [03](03_design_functional_requirements.md) §3.1.2b: MTTD, MTTA, MTTM, MTTR, plus a trend chart over the last N shifts (not just a single current number), and self-service deflection rate / KB match rate at triage (§8.10) — the clearest signal of whether the Knowledge Base loop is actually compounding over time.
- Since Prometheus metrics already exist server-side, this dashboard can largely be a purpose-built read view on top of existing `/metrics` data plus ticket-state timestamps, rather than a new subsystem.

## 8.7 Shared component: Attachments & Rich Content

Every text surface in the product (problem description, notes, transfer reason) supports the same two things:

- **Markdown with a raw/rendered toggle.** Fenced code blocks render as code; everything else is plain formatting (bold, lists, links). Default view is rendered; a small `</>` raw toggle switches to source, useful when copying a stack trace back out.
- **File attachments**, any common MIME type — screenshots (png/jpg), documents (pdf/docx), logs (txt/log), even short screen recordings if the org allows it. Attachments associate with either the ticket as a whole or a specific note, and inherit that note's internal/external visibility — an attachment on an internal note never appears in the Customer view.

Attachments render as thumbnails inline in the thread, not as a buried "files" tab — seeing the screenshot next to the message that mentions it is the point.

## 8.8 Shared component: AI-Assisted Drafting

A small "AI draft" button sits next to the free-text fields where writing from scratch is the friction point: the problem description on ticket submission, a resolution note, a transfer reason. Tapping it sends the ticket's history so far (description, notes, status changes) to the LLM and returns a drafted continuation the person can edit before posting — it never posts on their behalf.

- For a Customer, this drafts a clearer problem description from a rough first attempt ("checkout is broken sometimes" → a structured description with what's failing, when it started, how often).
- For an Engineer, this drafts a resolution summary or a transfer note from the ticket's accumulated diagnostic notes, so closing out a ticket doesn't mean re-typing what's already been said elsewhere in the thread.
- The draft always appears in the editable text field, never auto-submitted — the human reviews and posts.

## 8.9 Shared component: AI Ticket Intelligence Panel

This is the "zoom in and learn from the ticket" capability: a structured summary, distinct per side, that the LLM regenerates every time a new note is added, so it stays current without anyone having to maintain it by hand.

Behavior:

- Collapsed by default, opened with a small "+ AI summary" button on the ticket detail view — it does not clutter the thread for people who just want to read the raw notes.
- Re-triggered on every new note (from Customer, Engineer, or Agent) — each time, it regenerates the whole summary from the full ticket history, not just the newest note.
- The displayed panel is overwritten each time, not appended — you always see the latest synthesis, not a growing log of stale summaries.
- Every generation is stored as a versioned snapshot on the backend (tied to the triggering note's ID and timestamp), even though only the latest is shown. This is what makes the raw generations useful later: a dataset of (ticket history so far → structured extraction) pairs, which is exactly the shape needed to fine-tune or evaluate a better extraction model down the line.
- Every field is editable by Engineer. The panel is a starting draft, not a verdict — Engineer can tap any field (problem statement, diagnosis, mitigation, agenda items, all of it) and correct it directly.

How edits interact with regeneration:

- An edited field is marked "✎ edited by {Engineer}" and is treated as ground truth from then on — the next auto-regeneration (triggered by a new note) fills in other fields as usual but leaves an edited field alone, rather than silently overwriting a human correction.
- Engineer can still tap "↻ regenerate this field" on an edited field to let the AI take another pass at it, if new information in the thread makes the old correction stale.
- A human-edited value is stored as its own snapshot too, distinguished from an AI-generated one — a corrected extraction is closer to a gold label than a raw model guess, and is exactly what a future fine-tuning pass would want to learn from.

Customer-side fields (editable by Engineer): Symptom, What the user already tried, Problem statement, Expected outcome, Suggested to-do list, and Detected agenda items — a customer's message often bundles more than one ask (a password reset and a billing question and a feature request, all in one ticket); the panel splits these out as a checklist, each item tracked as fulfilled or open, regenerated the same way as the rest of the panel every time a new note lands. Engineer can add, merge, split, or mark an item done manually.

Engineer-side fields (editable by Engineer): Problem statement (restated from the Engineer's diagnostic angle), Validation, Diagnosis, Mitigation, Resolution, and Split suggestion — when the detected agenda items look unrelated (different queues, different urgency, different domains), the panel flags it and offers Engineer a "Suggest split" action (§8.5) rather than leaving mismatched asks bundled under one SLA clock. Engineer can also dismiss the suggestion if the items genuinely belong together.

```
[+ AI summary]              ← closed by default

▾ AI summary (regenerated 2m ago, after Alice's note)
  Problem: Edge gateway TLS cert expired, blocking checkout          [✎ edit]
  Validated: Confirmed via edge-gw-3 logs, not a client-side issue   [✎ edit]
  Diagnosis: Renewal cronjob silently failed 3 days ago
             ✎ edited by Alice: "cronjob AND missing alert — both"  [↻ regenerate]
  Mitigation: Manually renewed cert, checkout restored               [✎ edit]
  Resolution: pending — root-causing the cronjob failure             [✎ edit]

  Detected agenda items:
  ☑ Reset account password — done (Alice's note, 10:02)
  ☐ Question about invoice #4521 — unresolved
  ☐ Feature request: bulk export — unresolved
  ⚠ These look unrelated to the main issue.  [Suggest split ▾] [Dismiss]
```

Because this panel is regenerated wholesale rather than appended, it also naturally handles a Reopened ticket — the next regeneration picks up the new notes and reflects that the issue recurred, without anyone manually editing an old summary. The same wholesale-regeneration approach is what keeps the agenda checklist honest too: as each item gets addressed in a note, the next regeneration marks it fulfilled — and any field Engineer has corrected simply stays put until it's explicitly asked to regenerate again.

## 8.10 Shared component: Knowledge Base Feedback Loop

§8.9's panel captures structured data per ticket. This is the layer that makes it compound: every resolved ticket becomes a candidate contribution to a living Knowledge Base, and every new ticket can pull from anything already learned — not just "symptom → resolution," but the fuller diagnostic picture. Shipped in [v_2.1.0.md](../RELEASE/v_2.1.0.md); field list below is grounded in a research pass over KCS methodology (the industry-standard support-article structure: Issue/Environment/Cause/Resolution), ITIL known-error-database conventions (symptom, affected service, root cause, workaround, resolution), and incident-response "blast radius" terminology (scope of impact — users/transactions/services pulled in) — not invented from scratch.

**Customer-facing fields** (only ever shown once an article is published — §8.11's trust-boundary rule):

- **Symptom** — the customer's own words for the issue (KCS's "Issue" field).
- **What the customer should observe** — the recognizable signs of this issue, phrased for a future customer, not just this one (KCS's "Environment" adapted to a customer-facing framing).
- **Customer self-service steps** — the seed of this is already captured in §8.9 ("what the user already tried"); here it graduates into a maintained checklist other customers can try before filing at all.
- **Resolution** — the customer-facing fix/workaround copy.

**Engineer/internal-only fields** (never reach a Customer-facing surface, regardless of publish status):

- **Environment** — KCS's field proper: product/version/process this article applies to (distinct from the customer-facing "what to observe" above).
- **Root cause** — the underlying technical cause.
- **Validation steps** — not just the root cause, but how it was confirmed; this is the field the original design brief called out by name.
- **Resolution steps** — the actual technical fix procedure, distinct from the customer-facing Resolution copy above.
- **Workaround** — a temporary mitigation, distinct from the permanent Resolution (ITIL known-error-database convention: workarounds and permanent fixes are tracked separately).
- **Blast radius** — scope of impact for the incident this article documents (which users/transactions/services were pulled in); *which* Service(s) were impacted is a structured link (see §8.10a below), not free text here.

Article lifecycle:

1. Ticket resolves → the AI panel's final extraction (if AI is enabled and a snapshot exists; empty seed fields otherwise — the loop doesn't require AI) is proposed as a KB draft, either a new article or a diff against an existing similar one (matched via `KBService.MatchForSymptom` — simplest-first token-overlap scoring across published articles' symptom/what-to-observe fields; smarter matching is an enhancement, not a prerequisite).
2. Human curation gate. An Engineer or ServiceDeskAdmin reviews and approves before anything publishes — nothing auto-publishes to a customer-facing surface. `GET /kb/review` is the curation queue.
3. Published article stores every field above; `GET /kb` is the published, Customer-safe browse surface.
4. **Deferred** (not yet built, v_2.1.0.md scope note): surfacing the matching article proactively at two points — to the Customer at submission ("This looks like it might be: {article title}. Try these steps first?" — always with an easy "didn't help, file the ticket anyway" path), and to the Engineer at triage ("Similar past tickets: {article}"). `MatchForSymptom` exists and is unit-tested; only the UI wiring at these two screens is deferred. `GET /kb` (plain manual browse/search) is what ships in its place for now.

```
[Submitting ticket...]                              ⚠ deferred - not yet wired
⚡ This might be: "Checkout 504 after cert renewal window"
   Suggested steps: clear cache, retry after 5 min, check status page
   [Try these first]   [Not it — file my ticket]
```

The versioned snapshots in §8.9 matter beyond training a future model — they're also the raw material for this loop today, before any model retraining ever happens: a human curator can promote a good snapshot into a KB article manually from day one, and smarter suggestion-matching is an enhancement, not a prerequisite.

## 8.10a Shared component: Service catalog

A KB article's "which service is this about, and is it critical" needs a real entity behind it, not a free-text field — a lightweight, CMDB-style business-service catalog (`Service`), distinct from `Queue` (which team routes a ticket; `Service` is which business-facing system it's about). Common fields, per a research pass over CMDB/business-service-model conventions: `Name`, `Description`, `Criticality` (`Critical`/`High`/`Medium`/`Low` — matches the tiered-criticality convention most CMDB tools use, e.g. ServiceNow's "Most/Somewhat/Less/Not Critical"), `Status` (`active`/`deprecated`/`retired`), an optional `Owner` and `SupportQueue`, and a self-referencing `Parent` (same pattern as `Organization`/`Queue`, for a future component-service hierarchy, e.g. "Exchange Online" rolling up into "Mail Service").

A ticket's impacted service (`Ticket.ServiceID`) is optional and settable at both submission and triage — "unknown" is a normal, common state, never required. A KB article can name 0+ impacted services (`KBArticleService`, many-to-many — one incident can span multiple services); the article never duplicates a service's criticality as its own field, it's read live off the linked `Service` so it can't drift from the catalog.

CRUD for the Service catalog itself is SystemAdmin-only (`/admin/services`) — a system-configuration concern like Users/Webhooks/Workflows, not a day-to-day queue/SLA concern like Queue/CustomFieldDef (which Manager owns via `CapQueueOps`).

## 8.11 Cross-cutting UI rules

- One primary action per screen, always in the same top-right or top-left slot, per persona. Never make the user hunt.
- Status transitions are dropdowns of legal next-states only — shares validation with the state machine ([03](03_design_functional_requirements.md) §3.1.2).
- Internal vs external is never a checkbox. Always two distinct, visually separated actions.
- Default sort order is role-specific, not global: risk-sorted for ServiceDeskAdmin, stage-age-sorted for Engineer, submission-recency for Customer, aggregate/activity for Manager.
- Never hide a button — only reorder and resize it. Low-frequency controls (tagging, org settings, transfer reason) get smaller and lower in the layout for the roles that rarely touch them, but they're always one tap away, never behind a nested menu or a hover state.
- Every text field takes Markdown with a raw/rendered toggle, plus attachments. Consistent everywhere: problem description, notes, transfer reasons.
- The AI summary panel is the one deliberate exception to "never hide" — it's collapsed by default because it's a derived, regenerated artifact, not a primary action; a single "+" always reveals it.
- A human edit to the AI panel always outranks the next auto-regeneration. Engineer corrections are ground truth until Engineer explicitly asks for a fresh AI pass on that field.
- Nothing AI-generated reaches a customer-facing surface without a human curation gate. The AI panel (§8.9) is fine to show raw internally; the Knowledge Base (§8.10) is not — it publishes only after review.
- The progress dot-bar (§8.2) is the one shared visual language across all five personas' views — it's what makes this feel like one coherent product instead of five separate tools bolted together.
