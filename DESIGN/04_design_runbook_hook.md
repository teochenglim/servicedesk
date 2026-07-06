# ServiceDesk — Design 04: The Runbook Hook (Incident Automation)

A Runbook is a **multi-step, stateful automation** built on the same step interpreter as regular workflows (§03.5), designed to guide on-call Engineers through standardized incident responses. The only difference from a plain workflow is `is_runbook = true`, which typically means it's started manually from a ticket (`POST /tickets/{id}/runbooks/{workflowID}/start`) rather than firing automatically on `ticket_created`.

## 4.1 Step Types Used by Runbooks

| Step Type | System Behavior | Interaction Mode |
| :--- | :--- | :--- |
| `user_input` | Pauses and presents a dynamic form (fields defined in the step config) in the ticket view. | Blocking UI interaction. |
| `http_request` | Calls an external API (CI/CD, monitoring, ...) and saves the parsed JSON (or raw text) response into the workflow context under `save_response_to`. | Asynchronous, run by the worker. |
| `template_render` | Renders a Go `text/template` string against the accumulated context and posts the result as a ticket note (`output_target: ticket_external_note` or `ticket_internal_note`). | Automatic; agent reviews the resulting note. |
| `approval` | Creates an `Approval` row for a named role and pauses; a rejection stops the workflow. | Blocking, resumed by `/approvals/{id}/decide`. |

## 4.2 Example Definition

```json
{
  "steps": [
    {
      "id": "gather_info",
      "type": "user_input",
      "fields": [
        { "name": "service_name", "label": "Service", "required": true },
        { "name": "severity", "label": "Severity", "type": "select", "options": ["SEV0", "SEV1"] }
      ]
    },
    {
      "id": "fetch_deploys",
      "type": "http_request",
      "url": "https://ci.internal.com/deploys?service={{.service_name}}&limit=10",
      "save_response_to": "deploy_events"
    },
    {
      "id": "draft_slack",
      "type": "template_render",
      "template": ":rotating_light: Incident Alert\nService: {{.service_name}}\nRecent Deploys: {{range .deploy_events}}- {{.version}}\n{{end}}",
      "output_target": "ticket_external_note"
    }
  ]
}
```

## 4.3 Execution Flow (Worker Logic)

`workflow.Engine.execute` loops over `cfg.Steps` starting at `task.StepIndex`, running each step's `runStep` until one of:

1. **Pause**: a `user_input` or `approval` step returns `paused=true` → task saved as `waiting_user`, loop stops.
2. **Error**: the step returns an error → `Attempts` increments with exponential backoff; after `maxStepAttempts` (3) the task is marked `failed`.
3. **Done**: `StepIndex` reaches the end of `cfg.Steps` → task marked `done`.

**Resume** (`Engine.Resume`, called from `POST /workflow-tasks/{id}/resume` or `Engine.DecideApproval`) merges the submitted data into the task's JSON context and re-queues it **at the same step index** — it does *not* increment `StepIndex`. This matters: each pausing step type re-checks its own "have I already been given an answer?" marker in the context (`_resumed_<step_id>` for `user_input`, `_approval_decision_<step_id>` for `approval`) and decides for itself whether to continue forward or, for a rejected approval, return an error that stops the workflow. An earlier version of this code incremented `StepIndex` inside `Resume`, which skipped that check entirely and let a *rejected* approval silently continue to the next step anyway — covered by `TestApproval_RejectionStopsWorkflow` now.

## 4.4 Why Runbooks over Plain Webhooks

- **Stateful**: they wait for human input or an approval decision.
- **Data Enrichment**: pull data from monitoring/CI tools mid-flow.
- **Standardization**: enforce a consistent incident response shape, reducing MTTR.
