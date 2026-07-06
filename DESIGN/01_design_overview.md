# ServiceDesk — Design 01: Overview & Objectives

> **Core Philosophy**: Lightweight, horizontally scalable, single Go binary + static HTML (HTMX + Alpine.js). Modern ITSM principles (Event Sourcing, State Machine, Dynamic Forms, Workflow Hooks) while staying operationally simple and self-contained.

## 1.1 Context

An internal IT service management tool handling user requests (incidents, inquiries, changes) through a unified queue system, with **multi-tenant** support so one deployment can serve several customer organizations in isolation.

## 1.2 Primary Business KPIs

- **Reduce MTTD** (Mean Time to Detect): categorization, labels, monitoring integration.
- **Reduce MTTA** (Mean Time to Acknowledge): queue-based pickup + assignment.
- **Reduce MTTM** (Mean Time to Mitigate): knowledge sharing, collaboration notes, automation (Runbooks).
- **Reduce MTTR** (Mean Time to Resolve): strict closure criteria, SLA tracking.
- **Problem Management & RCA**: link recurring incidents to a Problem record and drive Root Cause Analysis.
- **Lightweight Automation**: built-in workflow engine for approvals, auto-routing, notifications, and Runbooks.

## 1.3 Document Map

| File | Covers |
| :--- | :--- |
| [02_design_roles_and_tenancy.md](02_design_roles_and_tenancy.md) | Roles, queue membership, multi-tenant Organizations |
| [03_design_functional_requirements.md](03_design_functional_requirements.md) | Tickets, state machine, notes, labels, search, SSE, problems |
| [04_design_runbook_hook.md](04_design_runbook_hook.md) | The workflow engine and the Runbook Hook automation feature |
| [05_design_non_functional_requirements.md](05_design_non_functional_requirements.md) | Performance, availability, security, extensibility |
| [06_design_technical_architecture.md](06_design_technical_architecture.md) | Stack, database dialects, deployment topology |

Release history and what's shipped in each version lives in [../RELEASE.md](../RELEASE.md), not here — this folder describes the system as designed, not a changelog.
