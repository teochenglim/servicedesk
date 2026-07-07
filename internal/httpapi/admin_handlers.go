package httpapi

import (
	"net/http"
	"strconv"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
)

func (s *Server) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	s.render.Render(w, "admin_index", s.base(r, "Admin"))
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
