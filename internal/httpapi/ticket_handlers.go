package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"servicedesk/internal/auth"
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
	// SudoBy is the real admin's username during a Sudo-as session
	// (DESIGN/02 §2.5), empty otherwise - drives the persistent acting-as
	// banner in layout.html. User.* above already reflects the sudo target.
	SudoBy string
	// AIEnabled gates the AI draft buttons/Intelligence Panel in templates
	// (DESIGN/08 §8.8-8.9) - off by default (Config.AIEnabled).
	AIEnabled bool
}

type userView struct {
	UserID      int64
	Username    string
	Role        models.Role
	CanQueueOps bool // Manager (or SystemAdmin via Sudo-as) - see DESIGN/02 §2.1.1
}

func (s *Server) base(r *http.Request, title string) baseData {
	c := middleware.ClaimsFrom(r.Context())
	if c == nil {
		return baseData{Title: title, DemoMode: s.demoMode, AIEnabled: s.aiEnabled}
	}
	b := baseData{Title: title, DemoMode: s.demoMode, AIEnabled: s.aiEnabled, User: &userView{
		UserID: c.UserID, Username: c.Username, Role: c.Role, CanQueueOps: c.Role.Can(models.CapQueueOps),
	}}
	if c.SudoByID != nil {
		b.SudoBy = c.SudoByUsername
	}
	return b
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
	Attachments  []models.Attachment
	// AISummary is nil when AI features are disabled or the panel has never
	// been generated for this ticket yet (DESIGN/08 §8.9).
	AISummary *service.SummarySnapshotView
	// Services backs the "impacted service" select on both the ticket-new
	// form and the ticket detail/triage view (RELEASE/v_2.1.0.md Service
	// catalog) - always includable, empty selection means "unknown."
	Services []models.Service
	// KBSuggestion is the triage-time "similar past tickets" match
	// (RELEASE/v_3.0.0.md) - nil when there's no AI panel/symptom yet, or
	// nothing clears KBService.MatchForSymptom's threshold.
	KBSuggestion *models.KBArticle
	// Error surfaces a failed action (bad upload, unresolved watcher email,
	// illegal transition) as an inline banner instead of a bare error page
	// (RELEASE/v_3.0.4.md) - set from the `?error=` query param that
	// redirectToTicketWithError appends.
	Error string
	// YourEmail pre-fills the "add watchers by email" field with the
	// viewer's own address (RELEASE/v_3.0.4.md) - empty when not viewing a ticket.
	YourEmail string
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
	// created or were added to watch. Engineer/Manager/SystemAdmin see every org.
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
	Queues     []models.Queue
	Services   []models.Service
	Categories []models.Category
	Error      string
}

func (s *Server) ticketNewPageData(r *http.Request, errMsg string) ticketNewData {
	queues, _ := s.queues.List()
	services, _ := s.serviceSvc.List()
	categories, _ := s.categorySvc.ListTopLevel()
	return ticketNewData{baseData: s.base(r, "New ticket"), Queues: queues, Services: services, Categories: categories, Error: errMsg}
}

func (s *Server) handleTicketNewPage(w http.ResponseWriter, r *http.Request) {
	s.render.Render(w, "ticket_new", s.ticketNewPageData(r, ""))
}

// parseServiceID reads the "service_id" form field (an <select> with a blank
// "Unknown" option) into a *int64 - empty string means unknown, matching
// Ticket.ServiceID's nullable "unknown is a normal state" semantics
// (RELEASE/v_2.1.0.md).
func parseServiceID(r *http.Request) (*int64, error) {
	raw := r.FormValue("service_id")
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// customFieldsFromForm collects every "cf_<name>" form field (rendered by
// custom_fields_fragment.html, RELEASE/v_3.0.0.md) into the plain map
// CreateTicketInput.CustomFields already expects - multi-value fields (a
// multiselect's several selected <option>s sharing one name) become a
// []string, everything else a single string.
func customFieldsFromForm(form url.Values) map[string]any {
	cf := map[string]any{}
	for key, vals := range form {
		name, ok := strings.CutPrefix(key, "cf_")
		if !ok || len(vals) == 0 {
			continue
		}
		if len(vals) > 1 {
			cf[name] = vals
		} else {
			cf[name] = vals[0]
		}
	}
	return cf
}

func (s *Server) handleTicketCreate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// Customer's form never renders the Queue field (RELEASE/v_3.0.5.md) - a
	// missing/unparseable queue_id falls back to the guaranteed default Queue
	// #1 (seedDefaultQueue in internal/db/db.go), not an error. Staff's form
	// always submits a valid selection, so this never overrides their choice.
	queueID, err := strconv.ParseInt(r.FormValue("queue_id"), 10, 64)
	if err != nil {
		queueID = 1
	}
	serviceID, err := parseServiceID(r)
	if err != nil {
		http.Error(w, "invalid service id", http.StatusBadRequest)
		return
	}
	t, err := s.ticketSvc.Create(claims, service.CreateTicketInput{
		Title:        r.FormValue("title"),
		Description:  r.FormValue("description"),
		Priority:     models.Priority(r.FormValue("priority")),
		QueueID:      queueID,
		Category:     r.FormValue("category"),
		ServiceID:    serviceID,
		CustomFields: customFieldsFromForm(r.Form),
	})
	if err != nil {
		data := s.ticketNewPageData(r, err.Error())
		s.render.Render(w, "ticket_new", data)
		return
	}
	// Optional watchers at creation (RELEASE/v_3.0.5.md) - the creator is
	// already auto-watched by TicketService.Create; a typo'd invite here
	// shouldn't block ticket creation, so unresolved emails are only logged,
	// unlike handleWatchersAdd's dedicated error-banner flow on the detail page.
	if raw := r.FormValue("watcher_emails"); raw != "" {
		emails := strings.FieldsFunc(raw, func(c rune) bool {
			return c == ',' || c == '\n' || c == '\r' || c == ' ' || c == '\t'
		})
		if unresolved, werr := s.ticketSvc.WatchByEmails(claims, t.ID, emails); werr != nil {
			s.log.Warn("ticket create: watch by emails failed", "ticket_id", t.ID, "err", werr)
		} else if len(unresolved) > 0 {
			s.log.Info("ticket create: some watcher emails did not resolve", "ticket_id", t.ID, "unresolved", unresolved)
		}
	}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- t.ID is our own DB-generated int64, not user input
	http.Redirect(w, r, "/tickets/"+strconv.FormatInt(t.ID, 10), http.StatusSeeOther)
}

// handleTicketServiceUpdate lets Engineer+ set/correct the ticket's impacted
// service at triage (RELEASE/v_2.1.0.md) - a dedicated small endpoint rather
// than folding into a generic field-edit form, matching this codebase's
// granular-action style (pickup/assign/mitigate are separate endpoints too).
func (s *Server) handleTicketServiceUpdate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	serviceID, err := parseServiceID(r)
	if err != nil {
		http.Error(w, "invalid service id", http.StatusBadRequest)
		return
	}
	if _, err := s.ticketSvc.UpdateFields(claims, id, service.UpdateFieldsInput{ServiceID: &serviceID}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectToTicket(w, r, id)
}

type waitingForm struct {
	WorkflowName string
	Fields       []workflow.FieldDef
}

// customerCanSeeTicket applies the Customer visibility rule (DESIGN/02 §2.3):
// unrestricted for staff; a Customer must have created the ticket or be
// watching it, and it must be in their own org. Shared by every route that
// takes a ticket (or an attachment's parent ticket) ID directly rather than
// always routing through handleTicketDetail.
func (s *Server) customerCanSeeTicket(claims *auth.Claims, t *models.Ticket) bool {
	if claims.Role != models.RoleCustomer {
		return true
	}
	if t.CreatorID == claims.UserID {
		return true
	}
	watching, err := s.watchers.IsWatching(t.ID, claims.UserID)
	return err == nil && watching && t.OrgID == claims.OrgID
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
	if !s.customerCanSeeTicket(claims, t) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
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
	attachments, err := s.attachmentSvc.ListVisibleForTicket(claims, id)
	if err != nil {
		s.log.Warn("ticket detail: could not load attachments", "ticket_id", id, "err", err)
	}

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
	var yourEmail string
	for _, u := range allUsers {
		userNames[u.ID] = u.Username
		userRoles[u.ID] = u.Role
		if u.ID == claims.UserID {
			yourEmail = u.Email
		}
	}

	var agents []models.User
	var workflows []models.Workflow
	var aiSummary *service.SummarySnapshotView
	var kbSuggestion *models.KBArticle
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
		// AI Ticket Intelligence Panel (DESIGN/08 §8.9) is Engineer-facing only.
		// gorm.ErrRecordNotFound just means it hasn't been generated yet.
		if s.aiEnabled {
			if snap, serr := s.aiSummarySvc.Latest(id); serr == nil {
				aiSummary = snap
			}
		}
		// Triage-time KB suggestion (DESIGN/08 §8.10, RELEASE/v_3.0.0.md) -
		// matched against the panel's symptom field when one exists; silently
		// skipped otherwise (e.g. AI disabled), same as the panel itself.
		if symptom := symptomFromSummary(aiSummary); symptom != "" {
			if match, score, merr := s.kbSvc.MatchForSymptom(symptom, ""); merr == nil && match != nil && score >= service.KBMatchThreshold {
				kbSuggestion = match
			}
		}
	}

	services, err := s.serviceSvc.List()
	if err != nil {
		s.log.Warn("ticket detail: could not load services", "err", err)
	}

	s.render.Render(w, "tickets_workspace", ticketsWorkspaceData{
		baseData: s.base(r, "Ticket #"+strconv.FormatInt(t.ID, 10)),
		Tickets:  tickets, Queues: queues, Filter: f, View: view,
		Ticket: t, Notes: notes, Events: events, Tags: tags, Agents: agents,
		Watching: watching, Workflows: workflows, WaitingTasks: waiting, WaitingForms: waitingForms,
		Approvals: approvals, UserNames: userNames, UserRoles: userRoles, Attachments: attachments,
		AISummary: aiSummary, Services: services, KBSuggestion: kbSuggestion,
		Error: r.URL.Query().Get("error"), YourEmail: yourEmail,
	})
}

// symptomFromSummary reads the AI Ticket Intelligence Panel's "symptom"
// field (SummaryFields is a generic map, not a dedicated struct field - see
// summaryFieldOrder in internal/service/aisummary.go) for the triage-time KB
// suggestion. Returns "" when there's no panel yet, same as an empty field.
func symptomFromSummary(view *service.SummarySnapshotView) string {
	if view == nil {
		return ""
	}
	for _, f := range view.Fields {
		if f.Key == "symptom" {
			return f.Value
		}
	}
	return ""
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

// handleTicketMitigate marks the stage-tracking overlay's Mitigate milestone
// (DESIGN/03 §3.1.2b) - does not change ticket Status, only stamps MitigatedAt
// and optionally posts a note in the same call.
func (s *Server) handleTicketMitigate(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	if _, err := s.ticketSvc.MarkMitigated(claims, id, r.FormValue("note")); err != nil {
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
		redirectToTicketWithError(w, r, id, "Could not post that note: "+err.Error())
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

// handleWatchersAdd lets the viewer invite a few other people (by email) to
// watch the ticket alongside them (RELEASE/v_3.0.4.md) - see
// TicketService.WatchByEmails for the resolution/authorization rules.
func (s *Server) handleWatchersAdd(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm()
	emails := strings.FieldsFunc(r.FormValue("emails"), func(c rune) bool {
		return c == ',' || c == '\n' || c == '\r' || c == ' ' || c == '\t'
	})
	unresolved, err := s.ticketSvc.WatchByEmails(claims, id, emails)
	if err != nil {
		redirectToTicketWithError(w, r, id, "Could not add watchers: "+err.Error())
		return
	}
	if len(unresolved) > 0 {
		redirectToTicketWithError(w, r, id, "No ServiceDesk account found for: "+strings.Join(unresolved, ", "))
		return
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

// redirectToTicketWithError keeps a failed action (bad upload, unresolved
// watcher email, illegal transition) inside the ticket page instead of
// navigating to a bare http.Error text response (RELEASE/v_3.0.4.md) - msg is
// carried as a query param and rendered as a dismissible banner by
// handleTicketDetail/tickets_workspace.html.
func redirectToTicketWithError(w http.ResponseWriter, r *http.Request, id int64, msg string) {
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- id is our own DB-generated int64, not user input
	http.Redirect(w, r, "/tickets/"+strconv.FormatInt(id, 10)+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
}

func humanSize(bytes int64) string {
	const mb = 1 << 20
	if bytes >= mb {
		return strconv.FormatInt(bytes/mb, 10) + "MB"
	}
	return strconv.FormatInt(bytes/1024, 10) + "KB"
}
