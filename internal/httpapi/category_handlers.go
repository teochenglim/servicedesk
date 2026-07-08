package httpapi

import (
	"net/http"
	"strconv"

	"servicedesk/internal/models"
)

type categoriesData struct {
	baseData
	Categories []models.Category
	Error      string
}

func (s *Server) categoriesPageData(r *http.Request, errMsg string) categoriesData {
	categories, err := s.categorySvc.List()
	if err != nil {
		s.log.Error("categories: list failed", "err", err)
	}
	return categoriesData{baseData: s.base(r, "Categories"), Categories: categories, Error: errMsg}
}

func (s *Server) handleCategoriesList(w http.ResponseWriter, r *http.Request) {
	s.render.Render(w, "admin_categories", s.categoriesPageData(r, ""))
}

func (s *Server) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	parentID, err := parseOptionalID(r, "parent_id")
	if err != nil {
		http.Error(w, "invalid parent id", http.StatusBadRequest)
		return
	}
	c := &models.Category{
		Name:                r.FormValue("name"),
		ParentID:            parentID,
		TitleTemplate:       r.FormValue("title_template"),
		DescriptionTemplate: r.FormValue("description_template"),
	}
	if err := s.categorySvc.Create(c); err != nil {
		s.log.Error("categories: create failed", "name", c.Name, "err", err)
		s.render.Render(w, "admin_categories", s.categoriesPageData(r, err.Error()))
		return
	}
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

func (s *Server) handleCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	c, err := s.categorySvc.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	parentID, err := parseOptionalID(r, "parent_id")
	if err != nil {
		http.Error(w, "invalid parent id", http.StatusBadRequest)
		return
	}
	c.Name = r.FormValue("name")
	c.ParentID = parentID
	c.TitleTemplate = r.FormValue("title_template")
	c.DescriptionTemplate = r.FormValue("description_template")
	if err := s.categorySvc.Update(c); err != nil {
		s.log.Error("categories: update failed", "id", id, "err", err)
		s.render.Render(w, "admin_categories", s.categoriesPageData(r, err.Error()))
		return
	}
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

func (s *Server) handleCategoryDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.categorySvc.Delete(id); err != nil {
		s.log.Error("categories: delete failed", "id", id, "err", err)
		http.Error(w, "could not delete category", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}
