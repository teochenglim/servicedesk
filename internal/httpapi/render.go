package httpapi

import (
	"fmt"
	"html/template"
	"net/http"
	"time"

	"servicedesk/internal/markdown"
	"servicedesk/internal/models"
	"servicedesk/internal/service"
	"servicedesk/web"
)

var funcMap = template.FuncMap{
	"markdown": markdown.Render,
	"add":      func(a, b int) int { return a + b },
	"assignedTo": func(assignee *int64, userID int64) bool {
		return assignee != nil && *assignee == userID
	},
	"slaState":      func(due *time.Time) string { return slaStateAt(due, time.Now()) },
	"slaLabel":      func(due *time.Time) string { return slaLabelAt(due, time.Now()) },
	"stageProgress": stageProgressFunc,
	"ordinal":       ordinal,
	"dict":          dict,
	"isInlineable":  service.IsInlineable,
}

// dict lets a template pass a small ad-hoc map literal into a {{template}}
// call (html/template has no map-literal syntax of its own), e.g.
// {{template "markdown_editor" (dict "Name" "description" "Value" .X "ID" "y")}}.
// Panics on an odd argument count or non-string key - both are template-authoring
// mistakes caught immediately at render time, not bad input to handle gracefully.
func dict(pairs ...any) map[string]any {
	if len(pairs)%2 != 0 {
		panic("dict: odd number of arguments")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			panic("dict: keys must be strings")
		}
		m[key] = pairs[i+1]
	}
	return m
}

// stageProgressFunc adapts stageProgress to work from a template whether the
// caller has a models.Ticket value (list-row range) or a *models.Ticket
// (detail pane's .Ticket field) - html/template can't overload by type, so
// this type-switches instead of needing two differently-named template funcs.
// plain selects the jargon-free Customer wording (DESIGN/08 §8.4).
func stageProgressFunc(v any, plain bool) stageBarData {
	switch t := v.(type) {
	case models.Ticket:
		return stageProgress(&t, time.Now(), plain)
	case *models.Ticket:
		return stageProgress(t, time.Now(), plain)
	default:
		return stageBarData{}
	}
}

// slaStateAt buckets an SLA due time into "ok"/"warning"/"breach" for chip
// styling. Split out from the funcMap closure so it's testable without
// depending on wall-clock time.
func slaStateAt(due *time.Time, now time.Time) string {
	if due == nil {
		return ""
	}
	remaining := due.Sub(now)
	switch {
	case remaining < 0:
		return "breach"
	case remaining < 2*time.Hour:
		return "warning"
	default:
		return "ok"
	}
}

func slaLabelAt(due *time.Time, now time.Time) string {
	if due == nil {
		return ""
	}
	remaining := due.Sub(now)
	if remaining < 0 {
		return "Breached " + humanDuration(-remaining) + " ago"
	}
	return humanDuration(remaining) + " left"
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
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
