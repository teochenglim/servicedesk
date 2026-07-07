# ServiceDesk — Design 02: Roles, Queue Membership & Multi-Tenancy

## 2.1 Roles

Support tier (Tier 1/2/3) is **not** a global permission rank — it's a function of which queue(s) an Engineer belongs to (e.g. a `tier1_queue` or an `Engineers` queue). Every Engineer has the same permissions; queue membership decides what they can pick up.

| Role | Description | Key Permissions |
| :--- | :--- | :--- |
| **Customer** | Submits tickets and tracks status. | Create tickets; view own tickets (+ same-org tickets they're added to watch); add *external* notes; close resolved tickets; watch/unwatch. Scoped to their Organization. |
| **Engineer** | Triage, troubleshooting, and resolution. | View/pick up tickets in queues they belong to; internal/external notes; labels; Problem Management; start Runbooks. Cannot assign/transfer tickets to *other* engineers. |
| **Manager** (renamed from QueueAdmin) | Owns queue structure and routes work day-to-day. | Engineer permissions + create/edit/archive queues and queue membership; set/adjust per-queue SLA targets; **assign/transfer tickets to any engineer** regardless of queue membership; sees every ticket across every queue and org. |
| **SystemAdmin** | Full system management — the entire ServiceDesk. | Everything: user management, organizations, webhooks, workflow definitions, Sudo-as (§2.5), **and** every Manager capability (queue CRUD, per-queue SLA targets, cross-queue assign/transfer) directly, with no Sudo-as required. Not org-scoped — sees everything. See §2.1.1. |

`Role.AtLeast(min)` ranks these `Customer(0) < Engineer(1) < Manager(2) < SystemAdmin(3)` — this ordering is for genuinely hierarchical checks only (e.g. "must be staff to add an internal note," "SystemAdmin or the ticket's creator may reopen a Closed ticket"). Capability checks (§2.1.1) are a separate mechanism, not derived from this rank.

### 2.1.1 Capabilities: SystemAdmin holds all of them

Queue CRUD, per-queue SLA targets, and cross-queue assign/transfer are gated by a capability check, `Role.Can(CapQueueOps)`, not `Role.AtLeast(Manager)` — `Manager` holds `CapQueueOps` as its native role capability, and **`Role.Can` unconditionally returns `true` for `SystemAdmin` regardless of which capability is asked about** ("SystemAdmin is the entire servicedesk" — `RELEASE/v_3.0.1.md`). Every other role must be listed explicitly per-capability in `capabilityRoles`; a role earns a capability by being named for it (or by being `SystemAdmin`), never by outranking another role via `AtLeast`.

This reverses an earlier version of this rule, which deliberately withheld `CapQueueOps` from `SystemAdmin` so Manager's queue/routing screens stayed Manager's alone unless someone was explicitly covering via Sudo-as. `SystemAdmin` is now the unambiguous top of the hierarchy: nothing in the system requires a Sudo-as session just to reach a capability-gated action. Sudo-as (§2.5) still exists, but for a narrower and still-real reason: acting *as a specific other person's identity* — e.g. `QueueMembership` (who counts as a member of a queue, for self-pickup purposes) is a per-user fact, not a capability, so a SystemAdmin who wants to pick up a ticket the way a *particular* Engineer would still needs to act as that Engineer.

The same "SystemAdmin holds it unconditionally" rule covers `CapSudo` and `CapUserAdmin` too — both were already SystemAdmin-exclusive, now expressed by the same general mechanism instead of being separate special cases.

## 2.2 Queue Membership ("personal" vs "role-based" queues)

A `Queue` is just a named row (e.g. `General`, `tier1_queue`, `Networking`) plus a `QueueMembership` join table of which Engineers belong to it. This one mechanism covers both:

- **Personal queue**: a queue with exactly one engineer as a member.
- **Role-based / pooled queue** (e.g. `tier1_queue`): a queue with many engineers as members; any of them can pick up from it.

Rules enforced by `service.TicketService`:
- **Pickup** (self-assign) requires the acting Engineer to be a member of the ticket's queue.
- **Assign/transfer to someone else** requires `Role.Can(CapQueueOps)` (§2.1.1: Manager, or SystemAdmin directly), and bypasses the membership check (a Manager or SystemAdmin can transfer a ticket into a queue the target engineer isn't yet a member of — that's "transfer", not "pickup").
- The `/tickets?view=my-queues` list view shows tickets across every queue an Engineer belongs to, so they can browse the pool before picking one up.

Queues also support hierarchical Parent/Child (`ParentID`) per DESIGN.md §3.2, with per-queue default Priority/Category.

## 2.3 Multi-Tenant Organizations

Customers log in with **organization name + username + password**; a username can belong to one or more organizations (`OrgMembership`), disambiguated at login by the org name supplied. Internal staff (Engineer/Manager/SystemAdmin) are **not** org-scoped — the org field is ignored for them, and they see every organization's tickets.

```
Organization (self-referencing via ParentID)
  └── OrgMembership (which users/customers can log into this org)
Ticket.OrgID  → the org a ticket belongs to (0 for staff-created tickets)
```

`ParentID` makes `Organization` self-referencing (like `Queue`) so today's flat list of orgs can grow into **Group → Company → Department** later without a schema rename: a "Department" is just an `Organization` whose `ParentID` points at a "Company" org, which in turn points at a "Group" org. This isn't enforced yet — it's a deliberate seam for a future release (see [../RELEASE.md](../RELEASE.md)).

### Ticket visibility rules

- **Customer**: only tickets where `ticket.OrgID == claims.OrgID` **and** (`ticket.CreatorID == claims.UserID` **or** the customer has been added as a **watcher** of that ticket). This is how "share a ticket with a coworker at the same company" works — no separate ACL table, just the existing Watch/Unwatch mechanism (§3.1.4) plus the org-match check.
- **Engineer / Manager / SystemAdmin**: unrestricted — "all for all".

### Login flow

1. User submits `org`, `username`, `password`.
2. Look up the user by username, check the password hash.
3. If `role == Customer`: resolve `org` by name, verify `OrgMembership(org.ID, user.ID)` exists. Fail closed (generic "invalid organization, username, or password") if the org doesn't exist or the user isn't a member — never reveal which part was wrong.
4. If not a Customer: org is ignored; `OrgID` claim is `0`.
5. Issue a JWT with `{uid, username, role, org_id}` claims.

## 2.4 Admin UI

- `/queues` — create queues, manage queue membership (add/remove engineers), set per-queue SLA targets. Requires `Role.Can(CapQueueOps)` (Manager, or SystemAdmin directly — no Sudo-as needed).
- `/admin/orgs` — create organizations, manage org membership (add/remove customers). SystemAdmin.
- `/admin/users` — create users and assign roles; each row also has a "Sudo as" action (§2.5). SystemAdmin.

## 2.5 Sudo-as

SystemAdmin can start a session **acting as** any other user — not a "view as" preview, but real actions performed and audit-logged under the *target's* identity. Since §2.1.1's reversal (`RELEASE/v_3.0.1.md`), Sudo-as is **not** what grants SystemAdmin capability-gated actions — `Role.Can` already gives SystemAdmin every capability directly. What Sudo-as still exists for: acting as a *specific other person's identity* for the things that are per-user facts rather than capabilities — most concretely, `QueueMembership` (self-pickup requires being an actual member of that queue; SystemAdmin holding `CapQueueOps` lets them manage membership/SLA/transfer for everyone, but doesn't make them personally a member of every queue), and generally any situation where the audit trail or a scoped view should reflect a *named* person rather than "the admin, using admin powers."

- **Starting a session** requires `Role.Can(CapSudo)` (SystemAdmin only). While active, every RBAC/queue-membership/ticket-visibility check evaluates against the target user's role and identity, not the admin's — so a sudo'd session into a Manager can do exactly what that Manager could do, no more, no less.
- **Audit trail**: every action taken during the session is logged with the target's user ID as the acting user (consistent with all other attribution in `event_logs`), plus a marker recording who is actually sudo'd in, so any single event row is self-describing without needing to reconstruct session start/end boundaries.
- **Ending a session**: only by explicit "return to ServiceDeskAdmin" — never a silent timeout. A persistent banner ("Acting as {name} ({role}) — return to ServiceDeskAdmin") is shown for the whole duration.
