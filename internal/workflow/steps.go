package workflow

// FieldDef describes one form field of a "user_input" step (DESIGN.md 4.2).
type FieldDef struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Type     string   `json:"type,omitempty"` // text|select
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
}

// Step is a single unit of a workflow/runbook definition. Only the fields
// relevant to Type are populated; unused fields are simply left zero.
type Step struct {
	ID   string `json:"id"`
	Type string `json:"type"` // user_input|http_request|template_render|notify|add_note|auto_assign|webhook|approval|condition

	// user_input
	Fields []FieldDef `json:"fields,omitempty"`

	// http_request
	URL            string `json:"url,omitempty"`
	Method         string `json:"method,omitempty"`
	SaveResponseTo string `json:"save_response_to,omitempty"`

	// template_render
	Template     string `json:"template,omitempty"`
	OutputTarget string `json:"output_target,omitempty"` // ticket_external_note|ticket_internal_note

	// add_note
	Body     string `json:"body,omitempty"`
	Internal bool   `json:"internal,omitempty"`

	// auto_assign
	AssigneeID *int64 `json:"assignee_id,omitempty"`

	// webhook (fires a named event through the standard webhook dispatcher)
	Event string `json:"event,omitempty"`

	// approval
	ApproverRole string `json:"approver_role,omitempty"`

	// notify
	Message string `json:"message,omitempty"`

	// condition: only continue if ctx[Field] == Equals
	Field  string `json:"field,omitempty"`
	Equals string `json:"equals,omitempty"`
}

type Config struct {
	Steps []Step `json:"steps"`
}
