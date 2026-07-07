# ServiceDesk — Design Index

The full design doc has been split into `DESIGN/` so each concern is easy to find and keep current as the system evolves. Start here:

1. [DESIGN/01_design_overview.md](DESIGN/01_design_overview.md) — context, objectives, KPIs
2. [DESIGN/02_design_roles_and_tenancy.md](DESIGN/02_design_roles_and_tenancy.md) — roles, queue membership, multi-tenant organizations
3. [DESIGN/03_design_functional_requirements.md](DESIGN/03_design_functional_requirements.md) — tickets, state machine, notes, labels, search, SSE, problems
4. [DESIGN/04_design_runbook_hook.md](DESIGN/04_design_runbook_hook.md) — the workflow engine and Runbook Hook automation
5. [DESIGN/05_design_non_functional_requirements.md](DESIGN/05_design_non_functional_requirements.md) — performance, security, observability, extensibility
6. [DESIGN/06_design_technical_architecture.md](DESIGN/06_design_technical_architecture.md) — stack, database dialects, deployment topology
7. [DESIGN/07_design_ui.md](DESIGN/07_design_ui.md) — Thoughtworks brand tokens, layout, and component design for the frontend
8. [DESIGN/08_design_ux.md](DESIGN/08_design_ux.md) — persona-driven UX: per-role home screens, Ticket Progress Bar, AI/KB shared components

Related docs:
- [README.md](README.md) — how to run it
- [ARCHITECTURE.md](ARCHITECTURE.md) — how the code is organized and how a request flows through it
- [RELEASE.md](RELEASE.md) — version history and roadmap
- [CLAUDE.md](CLAUDE.md) — where things go in this repo, for anyone (human or agent) working on it
