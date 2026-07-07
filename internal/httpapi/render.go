package httpapi

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"
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
	"slaState":          func(due *time.Time) string { return slaStateAt(due, time.Now()) },
	"slaLabel":          func(due *time.Time) string { return slaLabelAt(due, time.Now()) },
	"stageProgress":     stageProgressFunc,
	"ordinal":           ordinal,
	"dict":              dict,
	"isInlineable":      service.IsInlineable,
	"parseOptions":      parseOptions,
	"parseCustomFields": parseCustomFields,
}

// parseOptions unmarshals a CustomFieldDef.Options JSON array string (e.g.
// `["a","b"]`) into a plain []string for a dropdown/multiselect template
// range - malformed/empty JSON just yields no options rather than erroring
// the whole page render.
func parseOptions(raw string) []string {
	var opts []string
	_ = json.Unmarshal([]byte(raw), &opts)
	return opts
}

// parseCustomFields unmarshals Ticket.CustomFields (a JSON object string)
// for read-only display on the ticket detail page (RELEASE/v_3.0.0.md) -
// values can be a plain string or (for a multiselect field) an array, so
// this stringifies whatever comes back rather than assuming one shape.
func parseCustomFields(raw string) map[string]string {
	var vals map[string]any
	_ = json.Unmarshal([]byte(raw), &vals)
	out := make(map[string]string, len(vals))
	for k, v := range vals {
		switch t := v.(type) {
		case []any:
			parts := make([]string, len(t))
			for i, p := range t {
				parts[i] = fmt.Sprint(p)
			}
			out[k] = strings.Join(parts, ", ")
		default:
			out[k] = fmt.Sprint(t)
		}
	}
	return out
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
