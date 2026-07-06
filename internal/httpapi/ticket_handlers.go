package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"servicedesk/internal/middleware"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
	"servicedesk/internal/service"
	"servicedesk/internal/workflow"
)

type baseData struct {
	Title    string
	User     *userView
	DemoMode bool
}

type userView struct {
	UserID   int64
	Username string
	Role     models.Role
}

func (s *Server) base(r *http.Request, title string) baseData {
	c := middleware.ClaimsFrom(r.Context())
	if c == nil {
		return baseData{Title: title, DemoMode: s.demoMode}
	}
	return baseData{Title: title, DemoMode: s.demoMode, User: &userView{UserID: c.UserID, Username: c.Username, Role: c.Role}}
}

type ticketsWorkspaceData struct {
	baseData
	// list pane — always populated
	Tickets []models.Ticket
	Queues  []models.Queue
	Filter  repo.ListFilter
	View    string

	// detail pane — nil/zero when no ticket is selected (GET /tickets)
	Ticket       *models.Ticket
	Notes        []models.Note
	Events       []models.EventLog
	Tags         []models.Tag
	Agents       []models.User
	Watching     bool
	Workflows    []models.Workflow
	WaitingTasks []models.WorkflowTask
	WaitingForms map[int64]waitingForm
	Approvals    []models.Approval
	UserNames    map[int64]string
	UserRoles    map[int64]models.Role
}

// loadTicketsList builds the ticket list query from request filters/view and
// runs it. Shared by GET /tickets (list only) and GET /tickets/{id} (list +
// detail), so both keep the exact same filtering/scoping behavior.
func (s *Server) loadTicketsList(r *http.Request) (tickets []models.Ticket, queues []models.Queue, f repo.ListFilter, view string, err error) {
	claims := middleware.ClaimsFrom(r.Context())
	q := r.URL.Query()
	view = q.Get("view")

	f = repo.ListFilter{Limit: 100}
	if st := q.Get("status"); st != "" {
		f.Status = []string{st}
	}
	if p := q.Get("priority"); p != "" {
		f.Priority = []string{p}
	}
	if qid := q.Get("queue_id"); qid != "" {
		if n, perr := strconv.ParseInt(qid, 10, 64); perr == nil {
			f.QueueID = &n
		}
	}
	if lbl := q.Get("label"); lbl != "" {
		f.Label = lbl
	}
	if query := q.Get("q"); query != "" {
		f.Query = query
	}

	switch view {
	case "mine-created":
		f.CreatorID = &claims.UserID
	case "mine-assigned":
		f.AssigneeID = &claims.UserID
	case "watched":
		f.WatcherID = &claims.UserID
	case "my-queues":
		// Engineers browse the shared pool(s) they belong to (e.g. "tier1_queue"),
		// so they can pick up tickets nobody has claimed yet.
		ids, idsErr := s.queueMembers.ListQueueIDsForUser(claims.UserID)
		if idsErr != nil {
			s.log.Error("tickets: list queue memberships failed", "user_id", claims.UserID, "err", idsErr)
		}
		f.QueueIDs = ids
	}
	// Customers are multi-tenant scoped: only tickets in their org that they
	// created or were added to watch. Engineer/QueueAdmin/SystemAdmin see every org.
	if claims.Role == models.RoleCustomer {
		f.CustomerScope = &repo.CustomerScope{OrgID: claims.OrgID, UserID: claims.UserID}
	}

	tickets, err = s.ticketSvc.List(f)
	if err != nil {
		return nil, nil, f, view, err
	}
	queues, _ = s.queues.List()
	return tickets, queues, f, view, nil
}

func (s *Server) handleTicketsList(w http.ResponseWriter, r *http.Request) {
	tickets, queues, f, view, err := s.loadTicketsList(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "tickets_workspace", ticketsWorkspaceData{
		baseData: s.base(r, "Tickets"), Tickets: tickets, Queues: queues, Filter: f, View: view,
	})
}

type ticketNewData struct {
	baseData
	Queues []models.Queue
	Error  string
}

func (s *Server) handleTicketNewPage(w http.ResponseWriter, r *http.Request) {
	queues, _ := s.queues.List()
	s.render.Render(w, "ticket_new", ticketNewData{baseData: s.base(r, "New ticket"), Queues: queues})
}

func (s *Server) handleTicketCreate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	queueID, _ := strconv.ParseInt(r.FormValue("queue_id"), 10, 64)
	t, err := s.ticketSvc.Create(claims, service.CreateTicketInput{
		Title:       r.FormValue("title"),
		Description: r.FormValue("description"),
		Priority:    models.Priority(r.FormValue("priority")),
		QueueID:     queueID,
		Category:    r.FormValue("category"),
	})
	if err != nil {
		queues, _ := s.queues.List()
		s.render.Render(w, "ticket_new", ticketNewData{baseData: s.base(r, "New ticket"), Queues: queues, Error: err.Error()})
		return
	}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- t.ID is our own DB-generated int64, not user input
	http.Redirect(w, r, "/tickets/"+strconv.FormatInt(t.ID, 10), http.StatusSeeOther)
}

type waitingForm struct {
	WorkflowName string
	Fields       []workflow.FieldDef
}

func (s *Server) handleTicketDetail(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	t, err := s.ticketSvc.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if claims.Role == models.RoleCustomer && t.CreatorID != claims.UserID {
		// Same-org tickets they've been added to watch are visible too
		// (multi-tenant: "unless added by others within the same company").
		watching, wErr := s.watchers.IsWatching(id, claims.UserID)
		if wErr != nil || !watching || t.OrgID != claims.OrgID {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	tickets, queues, f, view, err := s.loadTicketsList(r)
	if err != nil {
		s.log.Warn("ticket detail: could not load ticket list pane", "err", err)
	}

	includeInternal := claims.Role != models.RoleCustomer
	notes, _ := s.noteSvc.ListForTicket(id, includeInternal)
	events, _ := s.events.ListForTicket(id)
	tags, _ := s.tags.ListForTicket(id)
	watching, _ := s.watchers.IsWatching(id, claims.UserID)
	waiting, _ := s.workflowTask.ListWaitingForTicket(id)
	approvals, _ := s.approvals.ListForTicket(id)

	waitingForms := map[int64]waitingForm{}
	for _, task := range waiting {
		wf, err := s.workflows.Get(task.WorkflowID)
		if err != nil {
			s.log.Warn("ticket detail: workflow lookup failed for waiting task", "task_id", task.ID, "workflow_id", task.WorkflowID, "err", err)
			continue
		}
		var cfg workflow.Config
		if err := json.Unmarshal([]byte(wf.Config), &cfg); err != nil {
			s.log.Warn("ticket detail: invalid workflow config", "workflow_id", wf.ID, "err", err)
			continue
		}
		if task.StepIndex < len(cfg.Steps) && cfg.Steps[task.StepIndex].Type == "user_input" {
			waitingForms[task.ID] = waitingForm{WorkflowName: wf.Name, Fields: cfg.Steps[task.StepIndex].Fields}
		}
	}

	allUsers, err := s.users.List()
	if err != nil {
		s.log.Warn("ticket detail: could not load users", "ticket_id", id, "err", err)
	}
	userNames := make(map[int64]string, len(allUsers))
	userRoles := make(map[int64]models.Role, len(allUsers))
	for _, u := range allUsers {
		userNames[u.ID] = u.Username
		userRoles[u.ID] = u.Role
	}

	var agents []models.User
	var workflows []models.Workflow
	if claims.Role.IsAgent() {
		for _, u := range allUsers {
			if u.Role.IsAgent() {
				agents = append(agents, u)
			}
		}
		wfs, _ := s.workflows.List()
		for _, wf := range wfs {
			if wf.IsRunbook && wf.Active {
				workflows = append(workflows, wf)
			}
		}
	}

	s.render.Render(w, "tickets_workspace", ticketsWorkspaceData{
		baseData: s.base(r, "Ticket #"+strconv.FormatInt(t.ID, 10)),
		Tickets:  tickets, Queues: queues, Filter: f, View: view,
		Ticket: t, Notes: notes, Events: events, Tags: tags, Agents: agents,
		Watching: watching, Workflows: workflows, WaitingTasks: waiting, WaitingForms: waitingForms,
		Approvals: approvals, UserNames: userNames, UserRoles: userRoles,
	})
}

func ticketIDFromPath(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func (s *Server) handleTicketTransition(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	action := service.Action(r.FormValue("action"))
	reason := r.FormValue("reason")
	if _, err := s.ticketSvc.Transition(claims, id, action, reason); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectToTicket(w, r, id)
}

func (s *Server) handleTicketPickup(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.ticketSvc.Pickup(claims, id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectToTicket(w, r, id)
}

func (s *Server) handleTicketAssign(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	assigneeID, err := strconv.ParseInt(r.FormValue("assignee_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid assignee", http.StatusBadRequest)
		return
	}
	if _, err := s.ticketSvc.Assign(claims, id, assigneeID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectToTicket(w, r, id)
}

func (s *Server) handleNoteCreate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	internal := r.FormValue("internal") == "on" && claims.Role != models.RoleCustomer
	if _, err := s.noteSvc.Add(claims, id, r.FormValue("body"), internal); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectToTicket(w, r, id)
}

func (s *Server) handleWatch(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.ticketSvc.Watch(claims.UserID, id); err != nil {
		s.log.Error("tickets: watch failed", "ticket_id", id, "user_id", claims.UserID, "err", err)
	}
	redirectToTicket(w, r, id)
}

func (s *Server) handleUnwatch(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.ticketSvc.Unwatch(claims.UserID, id); err != nil {
		s.log.Error("tickets: unwatch failed", "ticket_id", id, "user_id", claims.UserID, "err", err)
	}
	redirectToTicket(w, r, id)
}

func (s *Server) handleLabelAdd(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	kind := r.FormValue("kind")
	if kind != "rca" {
		kind = "incident"
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name != "" {
		if err := s.ticketSvc.AddLabel(claims, id, name, kind); err != nil {
			s.log.Error("tickets: add label failed", "ticket_id", id, "name", name, "kind", kind, "err", err)
		}
	}
	redirectToTicket(w, r, id)
}

func (s *Server) handleLabelRemove(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tagID, err := strconv.ParseInt(r.PathValue("tagID"), 10, 64)
	if err == nil {
		if err := s.ticketSvc.RemoveLabel(claims, id, tagID); err != nil {
			s.log.Error("tickets: remove label failed", "ticket_id", id, "tag_id", tagID, "err", err)
		}
	}
	redirectToTicket(w, r, id)
}

func redirectToTicket(w http.ResponseWriter, r *http.Request, id int64) {
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- id is our own DB-generated int64, not user input
	http.Redirect(w, r, "/tickets/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
