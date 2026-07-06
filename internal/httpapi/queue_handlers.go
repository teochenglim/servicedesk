package httpapi

import (
	"net/http"
	"strconv"

	"servicedesk/internal/models"
)

type queuesData struct {
	baseData
	Queues   []models.Queue
	Members  map[int64][]models.User
	AllUsers []models.User
	Error    string
}

func (s *Server) handleQueuesList(w http.ResponseWriter, r *http.Request) {
	queues, err := s.queues.List()
	if err != nil {
		s.log.Error("queues: list failed", "err", err)
		http.Error(w, "could not load queues", http.StatusInternalServerError)
		return
	}
	members := map[int64][]models.User{}
	for _, q := range queues {
		ms, err := s.queueMembers.ListMembers(q.ID)
		if err != nil {
			s.log.Error("queues: list members failed", "queue_id", q.ID, "err", err)
			continue
		}
		members[q.ID] = ms
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
		baseData: s.base(r, "Queues"), Queues: queues, Members: members, AllUsers: engineers,
	})
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
