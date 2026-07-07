package httpapi

import "net/http"

// handleCustomFieldsForCategory backs the ticket-new form's dynamic custom
// fields section (RELEASE/v_3.0.0.md) - an htmx partial swap fired on the
// free-text Category input's change event, since CustomFieldDef rows are
// looked up by exact category-string match (repo.CustomFieldRepo.ListForCategory).
func (s *Server) handleCustomFieldsForCategory(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	fields, err := s.customFields.ListForCategory(category)
	if err != nil {
		s.log.Error("custom fields: list for category failed", "category", category, "err", err)
		http.Error(w, "could not load custom fields", http.StatusInternalServerError)
		return
	}
	s.render.Render(w, "custom_fields_fragment", fields)
}
