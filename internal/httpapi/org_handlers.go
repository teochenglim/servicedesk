package httpapi

import (
	"net/http"
	"strconv"

	"servicedesk/internal/models"
)

type orgsData struct {
	baseData
	Orgs     []models.Organization
	Members  map[int64][]models.User
	AllUsers []models.User
	Error    string
}

// handleOrgsList is the SystemAdmin page for multi-tenant setup: create
// organizations and decide which Customer accounts can log into each one.
func (s *Server) handleOrgsList(w http.ResponseWriter, r *http.Request) {
	orgs, err := s.orgs.List()
	if err != nil {
		s.log.Error("orgs: list failed", "err", err)
		http.Error(w, "could not load organizations", http.StatusInternalServerError)
		return
	}
	members := map[int64][]models.User{}
	for _, o := range orgs {
		ms, err := s.orgMembers.ListMembers(o.ID)
		if err != nil {
			s.log.Error("orgs: list members failed", "org_id", o.ID, "err", err)
			continue
		}
		members[o.ID] = ms
	}
	allUsers, err := s.users.List()
	if err != nil {
		s.log.Error("orgs: list users failed", "err", err)
	}
	var customers []models.User
	for _, u := range allUsers {
		if u.Role == models.RoleCustomer {
			customers = append(customers, u)
		}
	}
	s.render.Render(w, "admin_orgs", orgsData{
		baseData: s.base(r, "Organizations"), Orgs: orgs, Members: members, AllUsers: customers,
	})
}

func (s *Server) handleOrgCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	o := &models.Organization{Name: r.FormValue("name")}
	if pid := r.FormValue("parent_id"); pid != "" {
		if n, err := strconv.ParseInt(pid, 10, 64); err == nil {
			o.ParentID = &n
		}
	}
	if err := s.orgs.Create(o); err != nil {
		s.log.Error("orgs: create failed", "name", o.Name, "err", err)
		orgs, _ := s.orgs.List()
		s.render.Render(w, "admin_orgs", orgsData{baseData: s.base(r, "Organizations"), Orgs: orgs, Error: err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/orgs", http.StatusSeeOther)
}

func (s *Server) handleOrgMemberAdd(w http.ResponseWriter, r *http.Request) {
	orgID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid user id", http.StatusBadRequest)
		return
	}
	if err := s.orgMembers.Add(orgID, userID); err != nil {
		s.log.Error("orgs: add member failed", "org_id", orgID, "user_id", userID, "err", err)
		http.Error(w, "could not add member", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/orgs", http.StatusSeeOther)
}

func (s *Server) handleOrgMemberRemove(w http.ResponseWriter, r *http.Request) {
	orgID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.orgMembers.Remove(orgID, userID); err != nil {
		s.log.Error("orgs: remove member failed", "org_id", orgID, "user_id", userID, "err", err)
		http.Error(w, "could not remove member", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/orgs", http.StatusSeeOther)
}
