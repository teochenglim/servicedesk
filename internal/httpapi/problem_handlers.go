package httpapi

import (
	"net/http"
	"strconv"

	"servicedesk/internal/models"
)

type problemsListData struct {
	baseData
	Problems []models.Problem
}

func (s *Server) handleProblemsList(w http.ResponseWriter, r *http.Request) {
	problems, err := s.problemSvc.List()
	if err != nil {
		s.log.Error("problems: list failed", "err", err)
		http.Error(w, "could not load problems", http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "problems_list", problemsListData{baseData: s.base(r, "Problems"), Problems: problems})
}

func (s *Server) handleProblemCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p := &models.Problem{
		Title:              r.FormValue("title"),
		RootCause:          r.FormValue("root_cause"),
		Resolution:         r.FormValue("resolution"),
		PreventiveMeasures: r.FormValue("preventive_measures"),
	}
	if err := s.problemSvc.Create(p); err != nil {
		s.log.Error("problems: create failed", "title", p.Title, "err", err)
		http.Error(w, "could not create problem", http.StatusInternalServerError)
		return
	}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- p.ID is our own DB-generated int64, not user input
	http.Redirect(w, r, "/problems/"+strconv.FormatInt(p.ID, 10), http.StatusSeeOther)
}

type problemDetailData struct {
	baseData
	Problem models.Problem
	Tickets []models.Ticket
	Error   string
}

func (s *Server) handleProblemDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.problemSvc.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	tickets, err := s.problemSvc.TicketsForProblem(id)
	if err != nil {
		s.log.Error("problems: list linked tickets failed", "problem_id", id, "err", err)
	}
	s.render.Render(w, "problem_detail", problemDetailData{
		baseData: s.base(r, "Problem #"+strconv.FormatInt(id, 10)), Problem: *p, Tickets: tickets,
	})
}

func (s *Server) handleProblemLink(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	ticketID, err := strconv.ParseInt(r.FormValue("ticket_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid ticket id", http.StatusBadRequest)
		return
	}
	if err := s.problemSvc.LinkTicket(id, ticketID, r.FormValue("rca_label")); err != nil {
		s.log.Error("problems: link ticket failed", "problem_id", id, "ticket_id", ticketID, "err", err)
		http.Error(w, "could not link ticket", http.StatusInternalServerError)
		return
	}
	// nosemgrep: go.lang.security.injection.open-redirect.open-redirect -- id is our own DB-generated int64, not user input
	http.Redirect(w, r, "/problems/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}
