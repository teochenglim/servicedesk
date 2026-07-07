package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"servicedesk/internal/middleware"
)

// sudoReturnCookie stashes the real admin's original session token while a
// Sudo-as session is active (DESIGN/02 §2.5), so "return to ServiceDeskAdmin"
// restores it directly instead of requiring a fresh login.
const sudoReturnCookie = "sd_return_token"

func setSessionCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(12 * time.Hour),
	})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

// handleSudoStart begins acting as another user: real actions, audit-logged
// under the target's identity (DESIGN/02 §2.5). The admin's own session
// token is stashed in sudoReturnCookie so "return" doesn't need a re-login.
func (s *Server) handleSudoStart(w http.ResponseWriter, r *http.Request) {
	admin := middleware.ClaimsFrom(r.Context())
	targetID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	token, _, err := s.sudoSvc.Start(admin, targetID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if current, cerr := r.Cookie("sd_token"); cerr == nil {
		setSessionCookie(w, sudoReturnCookie, current.Value)
	}
	setSessionCookie(w, "sd_token", token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleSudoStop ends the active sudo session and restores the admin's own
// token. Deliberately not gated by RequireCapability/RequireRole - while
// sudo'd in, claims reflect the *target's* role, which may be anything, so
// only the presence of SudoByID (checked inside SudoService.Stop) gates this.
func (s *Server) handleSudoStop(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if err := s.sudoSvc.Stop(claims); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	returnTok, err := r.Cookie(sudoReturnCookie)
	if err != nil {
		http.Error(w, "no return session found", http.StatusBadRequest)
		return
	}
	setSessionCookie(w, "sd_token", returnTok.Value)
	clearCookie(w, sudoReturnCookie)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
