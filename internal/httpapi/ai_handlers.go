package httpapi

import (
	"encoding/json"
	"net/http"
)

// handleAIDraftDescription drafts a ticket description from a rough first
// attempt, before any Ticket row exists (DESIGN/08 §8.8) - any authenticated
// user, since a Customer uses this at submission just as much as staff.
func (s *Server) handleAIDraftDescription(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	draft, err := s.aiDraftSvc.DraftDescription(r.FormValue("rough"))
	if err != nil {
		s.log.Warn("ai draft: description failed", "err", err)
		http.Error(w, "could not generate a draft", http.StatusBadGateway)
		return
	}
	writeJSONDraft(w, draft)
}

// handleAIDraft drafts a resolution summary or transfer note from an
// existing ticket's accumulated notes (DESIGN/08 §8.8).
func (s *Server) handleAIDraft(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var draft string
	switch r.FormValue("kind") {
	case "resolution":
		draft, err = s.aiDraftSvc.DraftResolutionNote(id)
	case "transfer":
		draft, err = s.aiDraftSvc.DraftTransferReason(id)
	default:
		http.Error(w, "unknown draft kind", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.log.Warn("ai draft: failed", "ticket_id", id, "kind", r.FormValue("kind"), "err", err)
		http.Error(w, "could not generate a draft", http.StatusBadGateway)
		return
	}
	writeJSONDraft(w, draft)
}

func writeJSONDraft(w http.ResponseWriter, draft string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"draft": draft})
}

// handleAISummaryRegenerate is the manual "regenerate the whole panel"
// action - useful the first time (before any note has triggered it) or to
// force a refresh.
func (s *Server) handleAISummaryRegenerate(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.aiSummarySvc.Regenerate(id, nil); err != nil {
		s.log.Warn("ai summary: manual regenerate failed", "ticket_id", id, "err", err)
		http.Error(w, "could not regenerate the AI summary", http.StatusBadGateway)
		return
	}
	redirectToTicket(w, r, id)
}

// handleAISummaryEditField records a human correction and locks that field
// against the next auto-regeneration (DESIGN/08 §8.9).
func (s *Server) handleAISummaryEditField(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	field := r.PathValue("field")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	if err := s.aiSummarySvc.EditField(id, field, r.FormValue("value")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectToTicket(w, r, id)
}

// handleAISummaryRegenerateField re-asks the model for one field and unlocks it.
func (s *Server) handleAISummaryRegenerateField(w http.ResponseWriter, r *http.Request) {
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	field := r.PathValue("field")
	if err := s.aiSummarySvc.RegenerateField(id, field); err != nil {
		s.log.Warn("ai summary: regenerate field failed", "ticket_id", id, "field", field, "err", err)
		http.Error(w, "could not regenerate this field", http.StatusBadGateway)
		return
	}
	redirectToTicket(w, r, id)
}
