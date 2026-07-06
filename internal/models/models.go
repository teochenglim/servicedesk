package models

import "time"

type Role string

// Engineer replaces the old Tier1/Tier2/Tier3Agent split: support tier is not
// a global permission rank, it's a function of which queue(s) an engineer
// belongs to (see QueueMembership). Every engineer has the same permissions;
// which tickets they can pick up/be assigned is gated by queue membership.
const (
	RoleCustomer    Role = "Customer"
	RoleEngineer    Role = "Engineer"
	RoleQueueAdmin  Role = "QueueAdmin"
	RoleSystemAdmin Role = "SystemAdmin"
)

func (r Role) IsAgent() bool {
	switch r {
	case RoleEngineer, RoleQueueAdmin, RoleSystemAdmin:
		return true
	}
	return false
}

func (r Role) AtLeast(min Role) bool {
	rank := map[Role]int{
		RoleCustomer:    0,
		RoleEngineer:    1,
		RoleQueueAdmin:  2,
		RoleSystemAdmin: 3,
	}
	return rank[r] >= rank[min]
}

type User struct {
	ID           int64     `gorm:"primaryKey" json:"id"`
	Username     string    `gorm:"uniqueIndex;size:190;not null" json:"username"`
	Email        string    `gorm:"not null;default:''" json:"email"`
	PasswordHash string    `gorm:"not null;default:''" json:"-"`
	Role         Role      `gorm:"not null" json:"role"`
	Source       string    `gorm:"not null;default:db" json:"source"` // db | static | ldap
	CreatedAt    time.Time `json:"created_at"`
}

// Organization is the multi-tenant boundary: Customers log in with an org
// name plus username/password and only see tickets scoped to their org (own
// tickets, plus tickets they've been added to watch within that org).
// Engineer/QueueAdmin/SystemAdmin are not org-scoped - they see everything.
//
// ParentID makes this self-referencing (like Queue) so today's flat list of
// orgs can grow into Group -> Company -> Department without a schema rename:
// a "Department" is just an Organization whose ParentID points at a "Company"
// org, which in turn can point at a "Group" org.
type Organization struct {
	ID       int64  `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"uniqueIndex;size:190;not null" json:"name"`
	ParentID *int64 `json:"parent_id"`
}

// OrgMembership is which organizations a user (almost always a Customer) can
// log into; a username can belong to 1+ orgs, disambiguated at login by the
// org name the user supplies alongside username/password.
type OrgMembership struct {
	OrgID  int64 `gorm:"primaryKey" json:"org_id"`
	UserID int64 `gorm:"primaryKey" json:"user_id"`
}

type Queue struct {
	ID              int64  `gorm:"primaryKey" json:"id"`
	Name            string `gorm:"not null" json:"name"`
	ParentID        *int64 `json:"parent_id"`
	DefaultPriority string `gorm:"not null;default:P3" json:"default_priority"`
	DefaultCategory string `gorm:"not null;default:''" json:"default_category"`
}

// QueueMembership is which Engineers belong to which queue (e.g. an
// "Engineers" or "Networking" queue) - pickup/assign is restricted to
// members of a ticket's queue (QueueAdmin/SystemAdmin bypass this).
type QueueMembership struct {
	QueueID int64 `gorm:"primaryKey" json:"queue_id"`
	UserID  int64 `gorm:"primaryKey" json:"user_id"`
}

type TicketStatus string

const (
	StatusNew        TicketStatus = "New"
	StatusInProgress TicketStatus = "In Progress"
	StatusResolved   TicketStatus = "Resolved"
	StatusClosed     TicketStatus = "Closed"
	StatusRejected   TicketStatus = "Rejected"
)

type Priority string

const (
	PriorityP1 Priority = "P1"
	PriorityP2 Priority = "P2"
	PriorityP3 Priority = "P3"
	PriorityP4 Priority = "P4"
)

type Ticket struct {
	ID int64 `gorm:"primaryKey" json:"id"`
	// Full-text search (3.7) is set up separately per dialect after AutoMigrate
	// (db.applySQLiteFTS / applyMySQLFTS) since a FULLTEXT index tag here would
	// be emitted literally against sqlite/postgres too and fail to parse.
	Title       string       `gorm:"not null" json:"title"`
	Description string       `gorm:"not null;default:''" json:"description"`
	Priority    Priority     `gorm:"not null;default:P3" json:"priority"`
	Status      TicketStatus `gorm:"not null;default:New;index" json:"status"`
	QueueID     int64        `gorm:"not null;index" json:"queue_id"`
	Category    string       `gorm:"not null;default:''" json:"category"`
	AssigneeID  *int64       `gorm:"index" json:"assignee_id"`
	CreatorID   int64        `gorm:"not null;index" json:"creator_id"`
	// OrgID scopes Customer visibility (multi-tenant); 0 for tickets created
	// by internal staff, who aren't tied to any organization.
	OrgID        int64      `gorm:"not null;default:0;index" json:"org_id"`
	CustomFields string     `gorm:"not null;default:'{}'" json:"custom_fields"` // JSON blob
	SLADueAt     *time.Time `json:"sla_due_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type CustomFieldDef struct {
	ID       int64  `gorm:"primaryKey" json:"id"`
	Category string `gorm:"not null;size:190;uniqueIndex:uq_custom_field" json:"category"`
	Name     string `gorm:"not null;size:190;uniqueIndex:uq_custom_field" json:"name"`
	Label    string `gorm:"not null" json:"label"`
	Type     string `gorm:"not null" json:"type"`                 // text|number|dropdown|date|multiselect
	Options  string `gorm:"not null;default:'[]'" json:"options"` // JSON array for dropdown/multiselect
	Required bool   `gorm:"not null;default:false" json:"required"`
}

type Tag struct {
	ID   int64  `gorm:"primaryKey" json:"id"`
	Name string `gorm:"not null;size:190;uniqueIndex:uq_tag" json:"name"`
	Kind string `gorm:"not null;default:incident;size:16;uniqueIndex:uq_tag" json:"kind"` // incident | rca
}

type TicketTag struct {
	TicketID int64 `gorm:"primaryKey" json:"ticket_id"`
	TagID    int64 `gorm:"primaryKey" json:"tag_id"`
}

type Note struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	TicketID  int64     `gorm:"not null;index" json:"ticket_id"`
	AuthorID  int64     `gorm:"not null" json:"author_id"`
	Body      string    `gorm:"not null" json:"body"`
	Internal  bool      `gorm:"not null;default:false" json:"internal"`
	CreatedAt time.Time `json:"created_at"`
}

type Watcher struct {
	TicketID int64 `gorm:"primaryKey" json:"ticket_id"`
	UserID   int64 `gorm:"primaryKey" json:"user_id"`
}

type Problem struct {
	ID                 int64     `gorm:"primaryKey" json:"id"`
	Title              string    `gorm:"not null" json:"title"`
	RootCause          string    `gorm:"not null;default:''" json:"root_cause"`
	Resolution         string    `gorm:"not null;default:''" json:"resolution"`
	PreventiveMeasures string    `gorm:"not null;default:''" json:"preventive_measures"`
	CreatedAt          time.Time `json:"created_at"`
}

type ProblemTicket struct {
	ProblemID int64 `gorm:"primaryKey" json:"problem_id"`
	TicketID  int64 `gorm:"primaryKey" json:"ticket_id"`
}

type Webhook struct {
	ID     int64  `gorm:"primaryKey" json:"id"`
	URL    string `gorm:"not null" json:"url"`
	Events string `gorm:"not null;default:''" json:"events"` // comma-separated event names
	Secret string `gorm:"not null;default:''" json:"-"`
	Active bool   `gorm:"not null;default:true" json:"active"`
}

// WebhookDelivery is the durable outbox row used by the webhook retry worker.
type WebhookDelivery struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	WebhookID int64     `gorm:"not null;index" json:"webhook_id"`
	Event     string    `gorm:"not null" json:"event"`
	Payload   string    `gorm:"not null" json:"payload"`
	Status    string    `gorm:"not null;default:pending;index" json:"status"` // pending|delivered|failed
	Attempts  int       `gorm:"not null;default:0" json:"attempts"`
	NextRunAt int64     `gorm:"not null" json:"next_run_at"` // Unix epoch seconds; always supplied by the app
	LastError string    `gorm:"not null;default:''" json:"last_error"`
	CreatedAt time.Time `json:"created_at"`
}

type Workflow struct {
	ID        int64  `gorm:"primaryKey" json:"id"`
	Name      string `gorm:"not null" json:"name"`
	Trigger   string `gorm:"column:trigger_event;not null" json:"trigger"` // ticket_created|status_changed|field_updated|note_added
	IsRunbook bool   `gorm:"not null;default:false" json:"is_runbook"`
	Config    string `gorm:"not null;default:'{}'" json:"config"` // JSON: rules or runbook steps
	Active    bool   `gorm:"not null;default:true" json:"active"`
}

type WorkflowTaskStatus string

const (
	TaskPending     WorkflowTaskStatus = "pending"
	TaskProcessing  WorkflowTaskStatus = "processing"
	TaskWaitingUser WorkflowTaskStatus = "waiting_user"
	TaskDone        WorkflowTaskStatus = "done"
	TaskFailed      WorkflowTaskStatus = "failed"
)

type WorkflowTask struct {
	ID         int64              `gorm:"primaryKey" json:"id"`
	WorkflowID int64              `gorm:"not null;index" json:"workflow_id"`
	TicketID   *int64             `gorm:"index" json:"ticket_id"`
	Status     WorkflowTaskStatus `gorm:"not null;default:pending;index:idx_wf_tasks_status" json:"status"`
	StepIndex  int                `gorm:"not null;default:0" json:"step_index"`
	Context    string             `gorm:"not null;default:'{}'" json:"context"` // JSON accumulated context
	Attempts   int                `gorm:"not null;default:0" json:"attempts"`
	Error      string             `gorm:"not null;default:''" json:"error"`
	// NextRunAt is a Unix-epoch second, not time.Time: sqlite's driver formats
	// bound time.Time params as RFC3339 ("T" separator), which never compares
	// correctly against CURRENT_TIMESTAMP's space-separated format.
	NextRunAt int64     `gorm:"not null;index:idx_wf_tasks_status" json:"next_run_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Approval struct {
	ID           int64      `gorm:"primaryKey" json:"id"`
	TicketID     int64      `gorm:"not null;index" json:"ticket_id"`
	Step         int        `gorm:"not null;default:0" json:"step"`
	ApproverRole Role       `gorm:"not null" json:"approver_role"`
	Status       string     `gorm:"not null;default:pending" json:"status"` // pending|approved|rejected
	DecidedBy    *int64     `json:"decided_by"`
	DecidedAt    *time.Time `json:"decided_at"`
	CreatedAt    time.Time  `json:"created_at"`
}

type EventLog struct {
	ID        int64     `gorm:"primaryKey" json:"id"`
	TicketID  *int64    `gorm:"index" json:"ticket_id"`
	ActorID   *int64    `json:"actor_id"`
	Event     string    `gorm:"not null" json:"event"`
	Details   string    `gorm:"not null;default:'{}'" json:"details"` // JSON
	CreatedAt time.Time `json:"created_at"`
}
