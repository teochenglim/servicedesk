# Demo mode

`make demo` (or `DEMO_MODE=true go run ./cmd/servicedesk`) seeds a realistic dataset on first boot — safe to restart, it only seeds when the DB is empty. `DEMO_RESET=true` (alongside `DEMO_MODE=true`) wipes and reseeds on every boot instead. See [internal/demo/seed.go](internal/demo/seed.go) for exactly what gets created; this file is the human-facing reference for logging in and poking at it.

A "DEMO MODE" badge appears in the top bar, with a "Reset demo data" button for SystemAdmins (`POST /admin/demo/reset`). The login page (`/login`) also shows a one-click picker for the four accounts below when demo mode is on.

## Accounts

| Role | Username | Password | Organization | Notes |
| :--- | :--- | :--- | :--- | :--- |
| SystemAdmin | `admin` | `admin123` | *(n/a — staff aren't org-scoped)* | Not part of the `demo.*` seed set — this is the separate bootstrap account `auth.Bootstrap` always creates when `SERVICEDESK_STATIC_USERS` isn't set, demo mode or not. |
| Manager | `demo.admin` | `demo1234` | *(n/a)* | Owns both demo queues (`CapQueueOps`) — lands on `/manager`. |
| Engineer | `demo.engineer1` | `demo1234` | *(n/a)* | Member of the "Service Desk" queue. |
| Engineer | `demo.engineer2` | `demo1234` | *(n/a)* | Member of the "Service Desk" queue. |
| Engineer | `demo.engineer3` | `demo1234` | *(n/a)* | Member of the "Network Ops" queue. |
| Engineer | `demo.engineer4` | `demo1234` | *(n/a)* | Member of the "Network Ops" queue. |
| Customer | `demo.customer1` | `demo1234` | Acme Corp | |
| Customer | `demo.customer2` | `demo1234` | Acme Corp | |
| Customer | `demo.customer3` | `demo1234` | Globex Inc | |
| Customer | `demo.customer4` | `demo1234` | Globex Inc | |
| Customer | `demo.customer5` | `demo1234` | Initech | |
| Customer | `demo.customer6` | `demo1234` | Initech | |

Customer logins need the **Organization** field on `/login`; internal staff (Engineer/Manager/SystemAdmin) leave it blank.

## Seeded data

- **Orgs**: Acme Corp, Globex Inc, Initech (2 customers each).
- **Queues**: "Service Desk" (default P3/general), "Network Ops" (default P2/network).
- **Service catalog** (`RELEASE/v_2.1.0.md`): Mail Service (Critical, Service Desk), VPN Gateway (High, Network Ops), Corporate Network (Medium, Network Ops), File Server (Medium, Service Desk), Guest WiFi (Low, Service Desk).
- **~15 tickets** across both queues/all statuses, several linked to a Service (some deliberately left unlinked — "unknown" is a normal state).
- **1 Problem** ("Recurring VPN/network instability") linking the 3 network-outage tickets.
- **1 Runbook** ("Demo: Auto-assign & notify") on the Network Ops queue.
- **2 Knowledge Base articles** (`RELEASE/v_2.1.0.md`): one **published** ("Vendor emails going to spam", linked to Mail Service — visible to everyone at `/kb`), one still a **draft** ("Printer shows offline in Windows" — visible only to staff at `/kb/review`, illustrating the curation gate before anything reaches a Customer).

## Verifying it end-to-end without a browser

`./scripts/demo.sh` (also `make demo-curl-test`) drives a running demo-mode server with `curl` through the same checks used to smoke-test each release — login as all 4 personas, custom fields on the ticket form, the Knowledge Base match endpoint, the Manager dashboard's MTTx trend sparklines, and the trust-boundary rule that an unapproved KB draft never reaches a Customer. Start the server first (`make demo` in one terminal), then run the script against it:

```
make demo-curl-test
# or: ./scripts/demo.sh [base_url, default http://localhost:8080]
```

It exits non-zero on the first failed check, printing which one failed.
