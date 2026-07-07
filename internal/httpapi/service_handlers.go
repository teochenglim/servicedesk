package httpapi

import (
	"net/http"
	"strconv"

	"servicedesk/internal/models"
)

type servicesData struct {
	baseData
	Services []models.Service
	Users    []models.User
	Queues   []models.Queue
	Error    string
}

func (s *Server) servicesPageData(r *http.Request, errMsg string) servicesData {
	services, err := s.serviceSvc.List()
	if err != nil {
		s.log.Error("services: list failed", "err", err)
	}
	users, err := s.users.List()
	if err != nil {
		s.log.Error("services: list users failed", "err", err)
	}
	queues, err := s.queues.List()
	if err != nil {
		s.log.Error("services: list queues failed", "err", err)
	}
	return servicesData{baseData: s.base(r, "Services"), Services: services, Users: users, Queues: queues, Error: errMsg}
}

func (s *Server) handleServicesList(w http.ResponseWriter, r *http.Request) {
	s.render.Render(w, "admin_services", s.servicesPageData(r, ""))
}

// parseOptionalID reads a form field into a *int64 - empty string means
// "unset" (e.g. Service.OwnerID/SupportQueueID/ParentID are all optional).
func parseOptionalID(r *http.Request, field string) (*int64, error) {
	raw := r.FormValue(field)
	if raw == "" {
		return nil, nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *Server) handleServiceCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ownerID, err := parseOptionalID(r, "owner_id")
	if err != nil {
		http.Error(w, "invalid owner id", http.StatusBadRequest)
		return
	}
	supportQueueID, err := parseOptionalID(r, "support_queue_id")
	if err != nil {
		http.Error(w, "invalid support queue id", http.StatusBadRequest)
		return
	}
	parentID, err := parseOptionalID(r, "parent_id")
	if err != nil {
		http.Error(w, "invalid parent id", http.StatusBadRequest)
		return
	}
	svc := &models.Service{
		Name:           r.FormValue("name"),
		Description:    r.FormValue("description"),
		Criticality:    models.ServiceCriticality(r.FormValue("criticality")),
		Status:         models.ServiceStatus(r.FormValue("status")),
		OwnerID:        ownerID,
		SupportQueueID: supportQueueID,
		ParentID:       parentID,
	}
	if svc.Criticality == "" {
		svc.Criticality = models.ServiceCriticalityMedium
	}
	if svc.Status == "" {
		svc.Status = models.ServiceStatusActive
	}
	if err := s.serviceSvc.Create(svc); err != nil {
		s.log.Error("services: create failed", "name", svc.Name, "err", err)
		s.render.Render(w, "admin_services", s.servicesPageData(r, err.Error()))
		return
	}
	http.Redirect(w, r, "/admin/services", http.StatusSeeOther)
}

func (s *Server) handleServiceUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	svc, err := s.serviceSvc.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ownerID, err := parseOptionalID(r, "owner_id")
	if err != nil {
		http.Error(w, "invalid owner id", http.StatusBadRequest)
		return
	}
	supportQueueID, err := parseOptionalID(r, "support_queue_id")
	if err != nil {
		http.Error(w, "invalid support queue id", http.StatusBadRequest)
		return
	}
	parentID, err := parseOptionalID(r, "parent_id")
	if err != nil {
		http.Error(w, "invalid parent id", http.StatusBadRequest)
		return
	}
	svc.Name = r.FormValue("name")
	svc.Description = r.FormValue("description")
	svc.Criticality = models.ServiceCriticality(r.FormValue("criticality"))
	svc.Status = models.ServiceStatus(r.FormValue("status"))
	svc.OwnerID = ownerID
	svc.SupportQueueID = supportQueueID
	svc.ParentID = parentID
	if err := s.serviceSvc.Update(svc); err != nil {
		s.log.Error("services: update failed", "id", id, "err", err)
		s.render.Render(w, "admin_services", s.servicesPageData(r, err.Error()))
		return
	}
	http.Redirect(w, r, "/admin/services", http.StatusSeeOther)
}

func (s *Server) handleServiceDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.serviceSvc.Delete(id); err != nil {
		s.log.Error("services: delete failed", "id", id, "err", err)
		http.Error(w, "could not delete service", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/services", http.StatusSeeOther)
}
