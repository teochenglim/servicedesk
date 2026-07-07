package httpapi

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"

	"servicedesk/internal/auth"
	"servicedesk/internal/middleware"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// auditRow adds display-friendly actor/sudo-by usernames to a raw EventLog
// row, for both the admin home's "recent audit events" and the full
// /admin/audit view (DESIGN/08 §8.3).
type auditRow struct {
	models.EventLog
	ActorName  string
	SudoByName string
}

func (s *Server) auditRows(events []models.EventLog) []auditRow {
	users, err := s.users.List()
	if err != nil {
		s.log.Error("audit: load user names failed", "err", err)
	}
	names := make(map[int64]string, len(users))
	for _, u := range users {
		names[u.ID] = u.Username
	}
	rows := make([]auditRow, len(events))
	for i, e := range events {
		row := auditRow{EventLog: e}
		if e.ActorID != nil {
			row.ActorName = names[*e.ActorID]
		}
		if e.SudoByID != nil {
			row.SudoByName = names[*e.SudoByID]
		}
		rows[i] = row
	}
	return rows
}

type adminIndexData struct {
	baseData
	Users        []models.User
	RecentEvents []auditRow
}

// handleAdminIndex is ServiceDeskAdmin's short home screen (DESIGN/08 §8.3):
// people and recent system activity, not operational/ticket noise - that
// belongs to Manager (/manager) and Engineer (/tickets).
func (s *Server) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.List()
	if err != nil {
		s.log.Error("admin index: list users failed", "err", err)
	}
	events, err := s.events.ListAudit(repo.AuditFilter{Limit: 10})
	if err != nil {
		s.log.Error("admin index: load recent audit events failed", "err", err)
	}
	s.render.Render(w, "admin_index", adminIndexData{
		baseData: s.base(r, "Admin"), Users: users, RecentEvents: s.auditRows(events),
	})
}

type auditLogData struct {
	baseData
	Events   []auditRow
	Filter   repo.AuditFilter
	ActorRaw string
}

// handleAuditLog is the full searchable/exportable system audit log
// (DESIGN/08 §8.3): filters by actor, event substring, and sudo-only, with a
// CSV export (?format=csv) alongside the normal HTML view.
func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := repo.AuditFilter{Event: q.Get("event"), SudoOnly: q.Get("sudo_only") == "on", Limit: 500}
	actorRaw := q.Get("actor_id")
	if actorRaw != "" {
		if id, err := strconv.ParseInt(actorRaw, 10, 64); err == nil {
			f.ActorID = &id
		}
	}
	events, err := s.events.ListAudit(f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := s.auditRows(events)

	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Disposition", `attachment; filename="audit.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{"created_at", "event", "actor_id", "actor_username", "sudo_by_id", "sudo_by_username", "ticket_id", "details"})
		for _, row := range rows {
			ticketID := ""
			if row.TicketID != nil {
				ticketID = strconv.FormatInt(*row.TicketID, 10)
			}
			actorID, sudoByID := "", ""
			if row.ActorID != nil {
				actorID = strconv.FormatInt(*row.ActorID, 10)
			}
			if row.SudoByID != nil {
				sudoByID = strconv.FormatInt(*row.SudoByID, 10)
			}
			_ = cw.Write([]string{
				row.CreatedAt.Format("2006-01-02 15:04:05"), row.Event, actorID, row.ActorName,
				sudoByID, row.SudoByName, ticketID, row.Details,
			})
		}
		cw.Flush()
		return
	}

	s.render.Render(w, "admin_audit", auditLogData{
		baseData: s.base(r, "Audit"), Events: rows, Filter: f, ActorRaw: actorRaw,
	})
}

type webhooksData struct {
	baseData
	Webhooks []models.Webhook
	Error    string
}

func (s *Server) handleWebhooksList(w http.ResponseWriter, r *http.Request) {
	hooks, err := s.webhooks.List()
	if err != nil {
		s.log.Error("webhooks: list failed", "err", err)
		http.Error(w, "could not load webhooks", http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "admin_webhooks", webhooksData{baseData: s.base(r, "Webhooks"), Webhooks: hooks})
}

func (s *Server) handleWebhookCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	hook := &models.Webhook{
		URL: r.FormValue("url"), Events: r.FormValue("events"),
		Secret: r.FormValue("secret"), Active: true,
	}
	if err := s.webhooks.Create(hook); err != nil {
		s.log.Error("webhooks: create failed", "url", hook.URL, "err", err)
		hooks, _ := s.webhooks.List()
		s.render.Render(w, "admin_webhooks", webhooksData{baseData: s.base(r, "Webhooks"), Webhooks: hooks, Error: err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
}

func (s *Server) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.webhooks.Delete(id); err != nil {
		s.log.Error("webhooks: delete failed", "id", id, "err", err)
		http.Error(w, "could not delete webhook", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/webhooks", http.StatusSeeOther)
}

type workflowsData struct {
	baseData
	Workflows []models.Workflow
	Error     string
}

func (s *Server) handleWorkflowsList(w http.ResponseWriter, r *http.Request) {
	wfs, err := s.workflows.List()
	if err != nil {
		s.log.Error("workflows: list failed", "err", err)
		http.Error(w, "could not load workflows", http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "admin_workflows", workflowsData{baseData: s.base(r, "Workflows"), Workflows: wfs})
}

func (s *Server) handleWorkflowCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	wf := &models.Workflow{
		Name: r.FormValue("name"), Trigger: r.FormValue("trigger"),
		IsRunbook: r.FormValue("is_runbook") == "on",
		Config:    r.FormValue("config"), Active: true,
	}
	if err := s.workflows.Create(wf); err != nil {
		s.log.Error("workflows: create failed", "name", wf.Name, "err", err)
		wfs, _ := s.workflows.List()
		s.render.Render(w, "admin_workflows", workflowsData{baseData: s.base(r, "Workflows"), Workflows: wfs, Error: err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/workflows", http.StatusSeeOther)
}

type usersData struct {
	baseData
	Users []models.User
	Roles []models.Role
	Error string
	// NewAPIToken is the plaintext token shown exactly once, right after
	// issuing it (see handleUserIssueAPIToken) - it is never persisted or
	// retrievable again, only its hash is stored.
	NewAPIToken string
}

var allRoles = []models.Role{
	models.RoleCustomer, models.RoleEngineer, models.RoleManager, models.RoleSystemAdmin, models.RoleAgent,
}

func (s *Server) handleUsersList(w http.ResponseWriter, r *http.Request) {
	users, err := s.users.List()
	if err != nil {
		s.log.Error("users: list failed", "err", err)
		http.Error(w, "could not load users", http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "admin_users", usersData{baseData: s.base(r, "Users"), Users: users, Roles: allRoles})
}

func (s *Server) handleUserCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	hash, err := auth.HashPassword(r.FormValue("password"))
	if err != nil {
		s.log.Error("users: hash password failed", "err", err)
		http.Error(w, "could not create user", http.StatusInternalServerError)
		return
	}
	u := &models.User{
		Username: r.FormValue("username"), Email: r.FormValue("email"),
		PasswordHash: hash, Role: models.Role(r.FormValue("role")), Source: "db",
	}
	if err := s.users.Create(u); err != nil {
		s.log.Error("users: create failed", "username", u.Username, "err", err)
		users, _ := s.users.List()
		s.render.Render(w, "admin_users", usersData{baseData: s.base(r, "Users"), Users: users, Roles: allRoles, Error: err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// handleUserRoleUpdate changes a user's role (DESIGN/08 §8.3's "set/change
// user roles"), audit-logged so it shows up in the recent-events feed the
// same way a sudo session does - this is exactly the kind of change that
// screen exists to surface.
func (s *Server) handleUserRoleUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	newRole := models.Role(r.FormValue("role"))
	target, err := s.users.GetByID(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	oldRole := target.Role
	if err := s.users.UpdateRole(id, newRole); err != nil {
		s.log.Error("users: update role failed", "user_id", id, "role", newRole, "err", err)
		http.Error(w, "could not update role", http.StatusInternalServerError)
		return
	}
	claims := middleware.ClaimsFrom(r.Context())
	details, _ := json.Marshal(map[string]any{"user_id": id, "username": target.Username, "from": oldRole, "to": newRole})
	if err := s.events.Append(&models.EventLog{ActorID: &claims.UserID, Event: "role_changed", Details: string(details), SudoByID: claims.SudoByID}); err != nil {
		s.log.Error("users: audit log for role change failed", "user_id", id, "err", err)
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// handleUserIssueAPIToken (re)issues a long-lived API token for a user -
// currently only meaningful for RoleAgent (DESIGN/08 §8.1). Issuing a new
// token replaces any previous one. The plaintext token is rendered back into
// the page exactly once; only its hash is ever persisted.
func (s *Server) handleUserIssueAPIToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	token, tokenID, tokenHash, err := auth.IssueAPIToken()
	if err != nil {
		s.log.Error("users: issue api token failed", "user_id", id, "err", err)
		http.Error(w, "could not issue token", http.StatusInternalServerError)
		return
	}
	if err := s.users.SetAPIToken(id, tokenID, tokenHash); err != nil {
		s.log.Error("users: save api token failed", "user_id", id, "err", err)
		http.Error(w, "could not save token", http.StatusInternalServerError)
		return
	}
	users, _ := s.users.List()
	s.render.Render(w, "admin_users", usersData{
		baseData: s.base(r, "Users"), Users: users, Roles: allRoles, NewAPIToken: token,
	})
}

type customFieldsData struct {
	baseData
	Fields []models.CustomFieldDef
	Error  string
}

func (s *Server) handleCustomFieldsList(w http.ResponseWriter, r *http.Request) {
	fields, err := s.customFields.List()
	if err != nil {
		s.log.Error("custom fields: list failed", "err", err)
		http.Error(w, "could not load custom fields", http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "admin_customfields", customFieldsData{baseData: s.base(r, "Custom Fields"), Fields: fields})
}

func (s *Server) handleCustomFieldCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	d := &models.CustomFieldDef{
		Category: r.FormValue("category"), Name: r.FormValue("name"), Label: r.FormValue("label"),
		Type: r.FormValue("type"), Options: r.FormValue("options"), Required: r.FormValue("required") == "on",
	}
	if d.Options == "" {
		d.Options = "[]"
	}
	if err := s.customFields.Create(d); err != nil {
		s.log.Error("custom fields: create failed", "category", d.Category, "name", d.Name, "err", err)
		fields, _ := s.customFields.List()
		s.render.Render(w, "admin_customfields", customFieldsData{baseData: s.base(r, "Custom Fields"), Fields: fields, Error: err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/custom-fields", http.StatusSeeOther)
}
