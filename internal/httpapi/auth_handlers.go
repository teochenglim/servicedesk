package httpapi

import (
	"net/http"
	"strings"
	"time"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
)

type loginPageData struct {
	Title string
	Error string
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	s.render.Render(w, "login", loginPageData{Title: "Log in"})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	orgName := strings.TrimSpace(r.FormValue("org"))
	username := r.FormValue("username")
	password := r.FormValue("password")

	u, err := s.users.GetByUsername(username)
	if err != nil || !auth.CheckPassword(u.PasswordHash, password) {
		s.render.Render(w, "login", loginPageData{Title: "Log in", Error: "Invalid organization, username, or password"})
		return
	}

	// Only Customers are org-scoped (multi-tenant); internal staff (Engineer/
	// Manager/SystemAdmin) see across every org, so the org field is unused for them.
	var orgID int64
	if u.Role == models.RoleCustomer {
		org, err := s.orgs.GetByName(orgName)
		if err != nil {
			s.render.Render(w, "login", loginPageData{Title: "Log in", Error: "Invalid organization, username, or password"})
			return
		}
		member, err := s.orgMembers.IsMember(org.ID, u.ID)
		if err != nil || !member {
			s.render.Render(w, "login", loginPageData{Title: "Log in", Error: "Invalid organization, username, or password"})
			return
		}
		orgID = org.ID
	}

	token, err := s.authMgr.IssueToken(*u, orgID)
	if err != nil {
		http.Error(w, "could not issue token", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "sd_token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(12 * time.Hour),
	})
	http.Redirect(w, r, "/tickets", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: "sd_token", Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
