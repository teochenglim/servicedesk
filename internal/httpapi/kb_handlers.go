package httpapi

import (
	"net/http"
	"strconv"

	"servicedesk/internal/middleware"
	"servicedesk/internal/models"
	"servicedesk/internal/service"
)

type kbListData struct {
	baseData
	Articles []models.KBArticle
}

// handleKBList is the published, customer-safe Knowledge Base browse surface
// (DESIGN/08 §8.10) - any authenticated role, published articles only.
// Submission-time/triage-time suggestion popups are a deferred follow-up
// (RELEASE/v_2.1.0.md); this plain browse/search page is what ships this pass.
func (s *Server) handleKBList(w http.ResponseWriter, r *http.Request) {
	articles, err := s.kbSvc.ListPublished()
	if err != nil {
		s.log.Error("kb: list published failed", "err", err)
		http.Error(w, "could not load knowledge base", http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "kb_list", kbListData{baseData: s.base(r, "Knowledge Base"), Articles: articles})
}

type kbReviewData struct {
	baseData
	Drafts []models.KBArticle
}

// handleKBReview is the human curation queue (DESIGN/08 §8.10 step 2) - the
// one gate between a proposed draft and the Customer-facing surface above.
func (s *Server) handleKBReview(w http.ResponseWriter, r *http.Request) {
	drafts, err := s.kbSvc.ListDrafts()
	if err != nil {
		s.log.Error("kb: list drafts failed", "err", err)
		http.Error(w, "could not load drafts", http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "kb_review", kbReviewData{baseData: s.base(r, "KB Review Queue"), Drafts: drafts})
}

type kbDetailData struct {
	baseData
	Article models.KBArticle
	// Services are the business services this article names as impacted
	// (RELEASE/v_2.1.0.md's Service catalog).
	Services []models.Service
	// CanEdit/AllServices back the curator edit form - unset for a Customer
	// viewing a published article.
	CanEdit     bool
	AllServices []models.Service
}

// handleKBDetail is gated by KBService.CanView - the one trust-boundary
// check that keeps an unapproved draft off any Customer-facing surface
// (DESIGN/08 §8.11): a draft 404s for anyone who isn't staff.
func (s *Server) handleKBDetail(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, err := s.kbSvc.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !s.kbSvc.CanView(claims, a) {
		http.NotFound(w, r)
		return
	}
	services, err := s.kbSvc.ServicesForArticle(id)
	if err != nil {
		s.log.Warn("kb detail: could not load impacted services", "id", id, "err", err)
	}

	data := kbDetailData{baseData: s.base(r, a.Title), Article: *a, Services: services}
	if claims.Role.IsAgent() {
		data.CanEdit = true
		data.AllServices, err = s.serviceSvc.List()
		if err != nil {
			s.log.Warn("kb detail: could not load services for edit form", "err", err)
		}
	}
	s.render.Render(w, "kb_detail", data)
}

func (s *Server) handleKBUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	// Always non-nil (even if empty): this form's checkbox group always
	// represents the article's full set of impacted services, so an empty
	// submission means "clear them all," not "leave services untouched."
	serviceIDs := make([]int64, 0, len(r.Form["service_ids"]))
	for _, raw := range r.Form["service_ids"] {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			serviceIDs = append(serviceIDs, v)
		}
	}

	in := service.KBArticleUpdate{
		Title: r.FormValue("title"), Symptom: r.FormValue("symptom"),
		WhatToObserve: r.FormValue("what_to_observe"), SelfServiceSteps: r.FormValue("self_service_steps"),
		Resolution: r.FormValue("resolution"), Environment: r.FormValue("environment"),
		RootCause: r.FormValue("root_cause"), ValidationSteps: r.FormValue("validation_steps"),
		ResolutionSteps: r.FormValue("resolution_steps"), Workaround: r.FormValue("workaround"),
		BlastRadius: r.FormValue("blast_radius"), ServiceIDs: serviceIDs,
	}
	if _, err := s.kbSvc.Update(id, in); err != nil {
		s.log.Error("kb: update failed", "id", id, "err", err)
		http.Error(w, "could not update article", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/kb/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// handleKBApprove is the human curation gate itself (DESIGN/08 §8.10 step 2) -
// draft -> published, after which handleKBList/handleKBDetail's CanView check
// lets it reach a Customer.
func (s *Server) handleKBApprove(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.kbSvc.Approve(claims.UserID, id); err != nil {
		s.log.Error("kb: approve failed", "id", id, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/kb/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) handleKBDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.kbSvc.Delete(id); err != nil {
		s.log.Error("kb: delete failed", "id", id, "err", err)
		http.Error(w, "could not delete article", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/kb/review", http.StatusSeeOther)
}
