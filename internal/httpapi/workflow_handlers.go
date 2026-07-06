package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"servicedesk/internal/middleware"
	"servicedesk/internal/models"
)

// handleRunbookStart manually kicks off a runbook workflow against a ticket
// (DESIGN.md 4: the on-call agent triggers the incident-runbook hook by hand).
func (s *Server) handleRunbookStart(w http.ResponseWriter, r *http.Request) {
	ticketID, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	workflowID, err := strconv.ParseInt(r.PathValue("workflowID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	wf, err := s.workflows.Get(workflowID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !wf.IsRunbook {
		http.Error(w, "not a runbook workflow", http.StatusBadRequest)
		return
	}
	if _, err := s.ticketSvc.Get(ticketID); err != nil {
		http.NotFound(w, r)
		return
	}
	task := &models.WorkflowTask{WorkflowID: wf.ID, TicketID: &ticketID, Status: models.TaskPending, Context: "{}", NextRunAt: time.Now().Unix()}
	if err := s.workflowTask.Create(task); err != nil {
		s.log.Error("runbook: start failed", "workflow_id", wf.ID, "ticket_id", ticketID, "err", err)
		http.Error(w, "could not start runbook", http.StatusInternalServerError)
		return
	}
	redirectToTicket(w, r, ticketID)
}

// handleWorkflowResume feeds a "user_input" step's submitted form fields back
// into a paused runbook task (DESIGN.md 4.2 Execution Flow, step 2: Resume).
func (s *Server) handleWorkflowResume(w http.ResponseWriter, r *http.Request) {
	taskID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	task, err := s.workflowTask.Get(taskID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	input := map[string]any{}
	for k, v := range r.Form {
		if len(v) == 1 {
			input[k] = v[0]
		} else {
			input[k] = v
		}
	}
	if err := s.engine.Resume(taskID, input); err != nil {
		s.log.Error("workflow: resume failed", "task_id", taskID, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if task.TicketID != nil {
		redirectToTicket(w, r, *task.TicketID)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleApprovalDecide records an agent's approve/reject decision and resumes
// any runbook task paused on it (DESIGN.md 3.5 Approval Processes).
func (s *Server) handleApprovalDecide(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	approve := r.FormValue("decision") == "approve"

	if err := s.approvals.Decide(id, claims.UserID, approve); err != nil {
		s.log.Error("approvals: decide failed", "approval_id", id, "err", err)
		http.Error(w, "could not record decision", http.StatusInternalServerError)
		return
	}
	if err := s.engine.DecideApproval(id, approve); err != nil {
		s.log.Error("approvals: resume workflow failed", "approval_id", id, "err", err)
	}

	a, err := s.approvals.Get(id)
	if err == nil {
		redirectToTicket(w, r, a.TicketID)
		return
	}
	w.WriteHeader(http.StatusOK)
}
