package httpapi

import (
	"html/template"
	"net/http"

	"servicedesk/internal/markdown"
	"servicedesk/web"
)

var funcMap = template.FuncMap{
	"markdown": markdown.Render,
	"add":      func(a, b int) int { return a + b },
	"assignedTo": func(assignee *int64, userID int64) bool {
		return assignee != nil && *assignee == userID
	},
}

func loadTemplates() *template.Template {
	return template.Must(template.New("").Funcs(funcMap).ParseFS(web.TemplatesFS, "templates/*.html"))
}

type Renderer struct {
	tmpl *template.Template
}

func NewRenderer() *Renderer {
	return &Renderer{tmpl: loadTemplates()}
}

func (rd *Renderer) Render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := rd.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
