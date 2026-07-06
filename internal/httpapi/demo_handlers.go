package httpapi

import (
	"net/http"

	"servicedesk/internal/demo"
)

// SetDemoMode records whether this instance is running in demo mode: gates
// the "DEMO MODE" badge, the reset button, and whether POST /admin/demo/reset
// is registered at all (see router.go).
func (s *Server) SetDemoMode(on bool) { s.demoMode = on }

// handleDemoReset wipes and reseeds the demo dataset (see internal/demo).
// Only reachable when demoMode is on and the caller is a SystemAdmin - see
// the conditional route registration in router.go.
func (s *Server) handleDemoReset(w http.ResponseWriter, r *http.Request) {
	if err := demo.Reset(s.db, s.log); err != nil {
		s.log.Error("demo: reset failed", "err", err)
		http.Error(w, "could not reset demo data", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
