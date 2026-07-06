package workflow

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"text/template"
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/auth"
	"servicedesk/internal/mailer"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

const maxStepAttempts = 3

// Publisher is the subset of service.EventPublisher the engine needs, kept
// local to avoid an import cycle between internal/service and internal/workflow.
type Publisher interface {
	Publish(ticketID int64, event string, payload any)
}

// WebhookDispatcher is the subset of webhook.Dispatcher the engine needs.
type WebhookDispatcher interface {
	Dispatch(event string, payload any)
}

// Engine executes both lightweight IF-THEN workflows and stateful Runbooks
// (DESIGN.md 3.5 and 4) from a single step-based interpreter.
type Engine struct {
	workflows *repo.WorkflowRepo
	tasks     *repo.WorkflowTaskRepo
	notes     *repo.NoteRepo
	tickets   *repo.TicketRepo
	approvals *repo.ApprovalRepo

	notifier Publisher
	webhooks WebhookDispatcher
	mail     *mailer.Mailer
	client   *http.Client
	log      *slog.Logger
}

func NewEngine(
	workflows *repo.WorkflowRepo, tasks *repo.WorkflowTaskRepo, notes *repo.NoteRepo,
	tickets *repo.TicketRepo, approvals *repo.ApprovalRepo,
	notifier Publisher, webhooks WebhookDispatcher, mail *mailer.Mailer, log *slog.Logger,
) *Engine {
	return &Engine{
		workflows: workflows, tasks: tasks, notes: notes, tickets: tickets, approvals: approvals,
		notifier: notifier, webhooks: webhooks, mail: mail,
		client: &http.Client{Timeout: 15 * time.Second}, log: log,
	}
}

// Trigger implements service.WorkflowTrigger: it enqueues one task per active
// workflow subscribed to triggerName. The worker loop (Run) executes them.
func (e *Engine) Trigger(triggerName string, ticketID int64, ctx map[string]any) {
	wfs, err := e.workflows.ListActiveForTrigger(triggerName)
	if err != nil {
		e.log.Error("workflow: list active failed", "err", err)
		return
	}
	ctxJSON, _ := json.Marshal(ctx)
	tid := ticketID
	for _, wf := range wfs {
		task := &models.WorkflowTask{
			WorkflowID: wf.ID, TicketID: &tid, Status: models.TaskPending,
			StepIndex: 0, Context: string(ctxJSON), NextRunAt: time.Now().Unix(),
		}
		if err := e.tasks.Create(task); err != nil {
			e.log.Error("workflow: enqueue task failed", "workflow_id", wf.ID, "err", err)
		}
	}
}

func (e *Engine) Run(ctx context.Context, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for e.ProcessOne() {
			}
		}
	}
}

func (e *Engine) ProcessOne() bool {
	task, err := e.tasks.ClaimNext()
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			e.log.Error("workflow: claim failed", "err", err)
		}
		return false
	}
	e.execute(task)
	return true
}

// Resume feeds user-submitted (or approval-decision) data back into a paused
// task and puts it back on the pending queue at the SAME step index: runStep
// re-enters that step (its "_resumed_"/"_approval_decision_" checks in ctxMap
// see the new data) and decides itself whether to advance or - for a
// rejected approval - stop the workflow. Incrementing StepIndex here would
// skip that check entirely and let a rejection continue anyway.
func (e *Engine) Resume(taskID int64, input map[string]any) error {
	task, err := e.tasks.Get(taskID)
	if err != nil {
		return err
	}
	if task.Status != models.TaskWaitingUser {
		return fmt.Errorf("task %d is not waiting for input", taskID)
	}
	ctxMap := map[string]any{}
	_ = json.Unmarshal([]byte(task.Context), &ctxMap)
	for k, v := range input {
		ctxMap[k] = v
	}
	merged, _ := json.Marshal(ctxMap)
	task.Context = string(merged)
	task.Status = models.TaskPending
	task.NextRunAt = time.Now().Unix()
	return e.tasks.Save(task)
}

func (e *Engine) execute(task *models.WorkflowTask) {
	wf, err := e.workflows.Get(task.WorkflowID)
	if err != nil {
		task.Status = models.TaskFailed
		task.Error = "workflow definition missing: " + err.Error()
		e.saveTask(task)
		return
	}
	var cfg Config
	if err := json.Unmarshal([]byte(wf.Config), &cfg); err != nil {
		task.Status = models.TaskFailed
		task.Error = "invalid workflow config: " + err.Error()
		e.saveTask(task)
		return
	}
	ctxMap := map[string]any{}
	_ = json.Unmarshal([]byte(task.Context), &ctxMap)

	for task.StepIndex < len(cfg.Steps) {
		step := cfg.Steps[task.StepIndex]
		paused, err := e.runStep(task, step, ctxMap)
		if err != nil {
			task.Attempts++
			task.Error = err.Error()
			if task.Attempts >= maxStepAttempts {
				task.Status = models.TaskFailed
				e.saveTask(task)
				e.log.Error("workflow: step failed permanently", "workflow_id", wf.ID, "step", step.ID, "err", err)
				return
			}
			task.Status = models.TaskPending
			task.NextRunAt = time.Now().Add(time.Duration(1<<task.Attempts) * time.Second).Unix()
			e.saveTask(task)
			return
		}
		if paused {
			ctxJSON, _ := json.Marshal(ctxMap)
			task.Context = string(ctxJSON)
			task.Status = models.TaskWaitingUser
			e.saveTask(task)
			return
		}
		task.StepIndex++
		task.Attempts = 0
	}

	ctxJSON, _ := json.Marshal(ctxMap)
	task.Context = string(ctxJSON)
	task.Status = models.TaskDone
	e.saveTask(task)
}

// saveTask persists task state and logs (rather than silently drops) any
// write failure - losing a status/step-index update would desync the
// in-memory execute() loop from what's durably recorded for the next poll.
func (e *Engine) saveTask(task *models.WorkflowTask) {
	if err := e.tasks.Save(task); err != nil {
		e.log.Error("workflow: task save failed", "task_id", task.ID, "workflow_id", task.WorkflowID, "status", task.Status, "err", err)
	}
}

// runStep executes one step, mutating ctxMap in place. It returns paused=true
// when the task must stop and wait for external input (user_input, approval).
func (e *Engine) runStep(task *models.WorkflowTask, step Step, ctxMap map[string]any) (paused bool, err error) {
	switch step.Type {
	case "condition":
		if fmt.Sprintf("%v", ctxMap[step.Field]) != step.Equals {
			// Skip remainder of this workflow by jumping past the end.
			task.StepIndex = 1 << 30
		}
		return false, nil

	case "user_input":
		if _, ok := ctxMap["_resumed_"+step.ID]; ok {
			return false, nil // already resumed with data
		}
		ctxMap["_resumed_"+step.ID] = true
		return true, nil

	case "http_request":
		return false, e.runHTTPRequest(step, ctxMap)

	case "template_render":
		out, err := renderTemplate(step.Template, ctxMap)
		if err != nil {
			return false, err
		}
		ctxMap[step.ID] = out
		return false, e.deliverRenderedOutput(task, step, out)

	case "add_note":
		if task.TicketID == nil {
			return false, nil
		}
		body, err := renderTemplate(step.Body, ctxMap)
		if err != nil {
			return false, err
		}
		return false, e.notes.Create(&models.Note{TicketID: *task.TicketID, AuthorID: auth.SystemActorID, Body: body, Internal: step.Internal})

	case "auto_assign":
		if task.TicketID == nil || step.AssigneeID == nil {
			return false, nil
		}
		t, err := e.tickets.Get(*task.TicketID)
		if err != nil {
			return false, err
		}
		t.AssigneeID = step.AssigneeID
		return false, e.tickets.Update(t)

	case "webhook":
		if e.webhooks != nil {
			e.webhooks.Dispatch(step.Event, ctxMap)
		}
		return false, nil

	case "notify":
		msg, err := renderTemplate(step.Message, ctxMap)
		if err != nil {
			return false, err
		}
		if e.notifier != nil && task.TicketID != nil {
			e.notifier.Publish(*task.TicketID, "workflow.notify", msg)
		}
		return false, nil

	case "approval":
		if approved, ok := ctxMap["_approval_decision_"+step.ID]; ok {
			if approved != true {
				return false, fmt.Errorf("rejected by approver")
			}
			return false, nil
		}
		if task.TicketID == nil {
			return false, fmt.Errorf("approval step requires a ticket")
		}
		a := &models.Approval{TicketID: *task.TicketID, Step: task.StepIndex, ApproverRole: models.Role(step.ApproverRole)}
		if err := e.approvals.Create(a); err != nil {
			return false, err
		}
		ctxMap["_pending_approval_id_"+step.ID] = a.ID
		return true, nil

	default:
		return false, fmt.Errorf("unknown step type %q", step.Type)
	}
}

func (e *Engine) runHTTPRequest(step Step, ctxMap map[string]any) error {
	url, err := renderTemplate(step.URL, ctxMap)
	if err != nil {
		return err
	}
	method := step.Method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("http_request step got status %s", resp.Status)
	}
	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		parsed = string(body) // not JSON; keep raw text available to templates
	}
	if step.SaveResponseTo != "" {
		ctxMap[step.SaveResponseTo] = parsed
	}
	return nil
}

func (e *Engine) deliverRenderedOutput(task *models.WorkflowTask, step Step, rendered string) error {
	if task.TicketID == nil {
		return nil
	}
	switch step.OutputTarget {
	case "ticket_external_note":
		return e.notes.Create(&models.Note{TicketID: *task.TicketID, AuthorID: auth.SystemActorID, Body: rendered, Internal: false})
	case "ticket_internal_note":
		return e.notes.Create(&models.Note{TicketID: *task.TicketID, AuthorID: auth.SystemActorID, Body: rendered, Internal: true})
	}
	return nil
}

func renderTemplate(tpl string, ctx map[string]any) (string, error) {
	t, err := template.New("step").Parse(tpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// DecideApproval is called by the approvals HTTP handler after an agent
// approves/rejects, so any paused runbook task resumes on the next poll.
func (e *Engine) DecideApproval(approvalID int64, approved bool) error {
	a, err := e.approvals.Get(approvalID)
	if err != nil {
		return err
	}
	if a.TicketID == 0 {
		return nil
	}
	waiting, err := e.tasks.ListWaitingForTicket(a.TicketID)
	if err != nil {
		return err
	}
	for _, t := range waiting {
		ctxMap := map[string]any{}
		_ = json.Unmarshal([]byte(t.Context), &ctxMap)
		for k, v := range ctxMap {
			if id, ok := toInt64(v); ok && id == approvalID && hasPrefix(k, "_pending_approval_id_") {
				stepID := k[len("_pending_approval_id_"):]
				if err := e.Resume(t.ID, map[string]any{"_approval_decision_" + stepID: approved}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	}
	return 0, false
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
