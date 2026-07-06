package httpapi

import (
	"encoding/json"
	"net/http"

	"gorm.io/gorm"
)

// SetDB wires the DB handle used for the /health readiness ping.
func (s *Server) SetDB(db *gorm.DB) { s.db = db }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	sqlDB, err := s.db.DB()
	if err != nil || sqlDB.Ping() != nil {
		s.log.Error("health: db ping failed", "err", err)
		status = "db unreachable"
		code = http.StatusServiceUnavailable
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": status})
}
