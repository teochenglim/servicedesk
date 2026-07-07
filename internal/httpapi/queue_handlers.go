package httpapi

import (
	"net/http"
	"strconv"

	"servicedesk/internal/middleware"
	"servicedesk/internal/models"
	"servicedesk/internal/sla"
)

type queuesData struct {
	baseData
	Queues     []models.Queue
	Members    map[int64][]models.User
	AllUsers   []models.User
	SLAMinutes map[int64]map[string]int // queueID -> priority -> effective minutes, for the edit form
	Error      string
}

// slaFormValues resolves a queue's effective SLA minutes for P1-P4, filling
// in sla.DefaultTable for anything the queue hasn't overridden, so the edit
// form always shows the value that's actually in effect.
func slaFormValues(table sla.Table) map[string]int {
	vals := make(map[string]int, 4)
	for _, p := range []string{"P1", "P2", "P3", "P4"} {
		minutes, _ := table.Evaluate(p, "")
		vals[p] = minutes
	}
	return vals
}

func (s *Server) handleQueuesList(w http.ResponseWriter, r *http.Request) {
	queues, err := s.queues.List()
	if err != nil {
		s.log.Error("queues: list failed", "err", err)
		http.Error(w, "could not load queues", http.StatusInternalServerError)
		return
	}
	members := map[int64][]models.User{}
	slaMinutes := map[int64]map[string]int{}
	for _, q := range queues {
		ms, err := s.queueMembers.ListMembers(q.ID)
		if err != nil {
			s.log.Error("queues: list members failed", "queue_id", q.ID, "err", err)
			continue
		}
		members[q.ID] = ms
		table, err := s.queueSvc.SLATable(q.ID)
		if err != nil {
			s.log.Error("queues: load SLA table failed", "queue_id", q.ID, "err", err)
			continue
		}
		slaMinutes[q.ID] = slaFormValues(table)
	}
	allUsers, err := s.users.List()
	if err != nil {
		s.log.Error("queues: list users failed", "err", err)
	}
	var engineers []models.User
	for _, u := range allUsers {
		if u.Role.IsAgent() {
			engineers = append(engineers, u)
		}
	}
	s.render.Render(w, "queues", queuesData{
		baseData: s.base(r, "Queues"), Queues: queues, Members: members, AllUsers: engineers, SLAMinutes: slaMinutes,
	})
}

// handleQueueSLAUpdate sets a queue's SLA targets from the P1-P4 minute
// inputs in the edit form (DESIGN/08 §8.6). Rows are Priority-only (Category
// blank/wildcard) for now - the sla.Table shape already supports adding
// category-specific rows later without a schema change.
func (s *Server) handleQueueSLAUpdate(w http.ResponseWriter, r *http.Request) {
	queueID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var table sla.Table
	for _, p := range []string{"P1", "P2", "P3", "P4"} {
		minutes, err := strconv.Atoi(r.FormValue("minutes_" + p))
		if err != nil || minutes <= 0 {
			http.Error(w, "invalid SLA minutes for "+p, http.StatusBadRequest)
			return
		}
		table = append(table, sla.Rule{Priority: p, Minutes: minutes})
	}
	claims := middleware.ClaimsFrom(r.Context())
	if err := s.queueSvc.SetSLATable(claims, queueID, table); err != nil {
		s.log.Error("queues: set SLA table failed", "queue_id", queueID, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/queues", http.StatusSeeOther)
}

func (s *Server) handleQueueCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	q := &models.Queue{
		Name:            r.FormValue("name"),
		DefaultPriority: r.FormValue("default_priority"),
		DefaultCategory: r.FormValue("default_category"),
	}
	if pid := r.FormValue("parent_id"); pid != "" {
		if n, err := strconv.ParseInt(pid, 10, 64); err == nil {
			q.ParentID = &n
		}
	}
	if err := s.queues.Create(q); err != nil {
		s.log.Error("queues: create failed", "name", q.Name, "err", err)
		queues, _ := s.queues.List()
		s.render.Render(w, "queues", queuesData{baseData: s.base(r, "Queues"), Queues: queues, Error: err.Error()})
		return
	}
	http.Redirect(w, r, "/queues", http.StatusSeeOther)
}

func (s *Server) handleQueueDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.queues.Delete(id); err != nil {
		s.log.Error("queues: delete failed", "id", id, "err", err)
		http.Error(w, "could not delete queue", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/queues", http.StatusSeeOther)
}

func (s *Server) handleQueueMemberAdd(w http.ResponseWriter, r *http.Request) {
	queueID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
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
	if err := s.queueMembers.Add(queueID, userID); err != nil {
		s.log.Error("queues: add member failed", "queue_id", queueID, "user_id", userID, "err", err)
		http.Error(w, "could not add member", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/queues", http.StatusSeeOther)
}

func (s *Server) handleQueueMemberRemove(w http.ResponseWriter, r *http.Request) {
	queueID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.queueMembers.Remove(queueID, userID); err != nil {
		s.log.Error("queues: remove member failed", "queue_id", queueID, "user_id", userID, "err", err)
		http.Error(w, "could not remove member", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/queues", http.StatusSeeOther)
}
