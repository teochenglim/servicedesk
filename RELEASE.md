# Release History

This project isn't under version control yet (no git repo, no tags) — the entries below track the *planned* release scope and what's actually been built against each, so that becomes traceable once tagging starts.

Newest first.

| Version | Theme | Status |
| :--- | :--- | :--- |
| [v3.1.0](RELEASE/v_3.1.0.md) | Larger initiatives (needs design pass) | Not started |
| [v3.0.8](RELEASE/v_3.0.8.md) | Favicon | Built — see file for exact scope |
| [v3.0.7](RELEASE/v_3.0.7.md) | Live ticket updates were completely dead (SSE middleware `http.Flusher` gap) | Built — see file for exact scope |
| [v3.0.6](RELEASE/v_3.0.6.md) | Markdown editor was never actually mounting (wrong vendored Toast UI Editor build) | Built — see file for exact scope |
| [v3.0.5](RELEASE/v_3.0.5.md) | Ticket submission form: Category catalog + templates, drop Queue field for Customer, watchers at creation | Built — see file for exact scope |
| [v3.0.4](RELEASE/v_3.0.4.md) | Customer view fixes (nav, close/reopen gating, multi-watcher, upload errors, empty details panel) | Built — see file for exact scope |
| [v3.0.3](RELEASE/v_3.0.3.md) | Semgrep SAST CI fix | Built — see file for exact scope |
| [v3.0.2](RELEASE/v_3.0.2.md) | Admin sidebar submenu | Built — see file for exact scope |
| [v3.0.1](RELEASE/v_3.0.1.md) | SystemAdmin is the entire servicedesk (nav + capability-model fix) | Built — see file for exact scope |
| [v3.0.0](RELEASE/v_3.0.0.md) | Quick-wins backlog | Built — see file for exact scope (swipe gestures scoped out entirely, tracked in backlog) |
| [v2.1.0](RELEASE/v_2.1.0.md) | Knowledge Base feedback loop + Service catalog | Built — see file for exact scope (suggestion popups deferred, then built in v3.0.0; external sync still deferred); `VERSION` not yet bumped from 2.0.0 |
| [v2.0.0](RELEASE/v_2.0.0.md) | Webhooks + MTTx metrics integration | Built — see file for exact scope (trend chart deferred, then built in v3.0.0) |
| [v1.0.12](RELEASE/v_1.0.12.md) | Persona UX redesign: AI-assisted drafting + AI Ticket Intelligence Panel | Built — see file for exact scope (off by default; agenda items/Suggest Split deferred) |
| [v1.0.11](RELEASE/v_1.0.11.md) | Persona UX redesign: Customer view, Manager role, ServiceDeskAdmin + Sudo-as | Built — see file for exact scope (run `/security-review` before merging) |
| [v1.0.10](RELEASE/v_1.0.10.md) | Persona UX redesign: RBAC foundation, stage tracking, attachments & rich text | Built — see file for exact scope |
| [v1.0.9](RELEASE/v_1.0.9.md) | README screenshot | Built — see file for exact scope |
| [v1.0.8](RELEASE/v_1.0.8.md) | Demo mode | Built — see file for exact scope |
| [v1.0.7](RELEASE/v_1.0.7.md) | Thoughtworks brand UI redesign | Built — see file for exact scope |
| [v1.0.0](RELEASE/v_1.0.0.md) | ServiceDesk core: UI, multi-tenant, multi-user login | Built — see file for exact scope |

Each version file lists what shipped, what's explicitly deferred, and any bugs found + fixed along the way (useful context for future changes in that area).
