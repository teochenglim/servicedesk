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
	RoleManager     Role = "Manager" // renamed from QueueAdmin (DESIGN/02 §2.1.1)
	RoleSystemAdmin Role = "SystemAdmin"
	// RoleAgent is the non-human automation/monitoring actor (DESIGN/08 §8.1) -
	// a real User row (not synthetic), so it reuses all existing ActorID/
	// CreatorID/AuthorID attribution. Authenticates via a long-lived API token
	// (see auth.IssueAPIToken), not the human JWT+cookie login flow.
	RoleAgent Role = "Agent"
)

// IsAgent is a legacy name predating the RoleAgent persona above - despite
// the naming collision, it means "is internal staff" (can see internal notes,
// pick up tickets, etc.), not "is the automation Agent." RoleAgent is
// deliberately included here since it shares that staff-level surface.
func (r Role) IsAgent() bool {
	switch r {
	case RoleEngineer, RoleManager, RoleSystemAdmin, RoleAgent:
		return true
	}
	return false
}

// AtLeast ranks roles for genuinely hierarchical checks only (e.g. "must be
// staff to add an internal note"). It is NOT used to gate queue ownership -
// see Capability/Can below and DESIGN/02 §2.1.1 for why a linear rank can't
// safely express "SystemAdmin outranks Manager but doesn't inherit Manager's
// queue-ops capability." RoleAgent sits at the same rank as Engineer, sharing
// its baseline staff surface (DESIGN/08 §8.1: "same API and state machine as
// everyone else").
func (r Role) AtLeast(min Role) bool {
	rank := map[Role]int{
		RoleCustomer:    0,
		RoleEngineer:    1,
		RoleAgent:       1,
		RoleManager:     2,
		RoleSystemAdmin: 3,
	}
	return rank[r] >= rank[min]
}

// Capability gates actions that don't follow the linear role rank - a role
// either has one or it doesn't, regardless of where it sits in AtLeast's
// ordering. See DESIGN/02 §2.1.1/§2.5.
type Capability string

const (
	// CapQueueOps: queue CRUD, per-queue SLA targets, cross-queue assign/transfer.
	CapQueueOps Capability = "queue_ops"
	// CapSudo: start/stop a Sudo-as session (net-new, DESIGN/02 §2.5).
	CapSudo Capability = "sudo"
	// CapUserAdmin: create/edit/deactivate users, role changes.
	CapUserAdmin Capability = "user_admin"
	// CapAgentDetect: backdate Ticket.DetectedAt to an earlier monitoring
	// trigger time at creation (DESIGN/03 §3.1.2b) - an ordinary Customer or
	// Engineer backdating this would corrupt the MTTD metric.
	CapAgentDetect Capability = "agent_detect"
)

var capabilityRoles = map[Capability]map[Role]bool{
	CapQueueOps:    {RoleManager: true},
	CapSudo:        {RoleSystemAdmin: true},
	CapUserAdmin:   {RoleSystemAdmin: true},
	CapAgentDetect: {RoleAgent: true},
}

// Can reports whether r holds the given capability. SystemAdmin holds every
// capability unconditionally - "SystemAdmin is the entire servicedesk," the
// top of the hierarchy, with nothing requiring Sudo-as to reach (RELEASE/v_3.0.1.md).
// This reverses the earlier DESIGN/02 §2.1.1 split where SystemAdmin didn't
// automatically pass CapQueueOps just by outranking Manager; every other
// role must still be listed explicitly in capabilityRoles, so this is not
// otherwise monotonic with AtLeast - Manager holding CapQueueOps still
// doesn't imply Engineer does, for example.
func (r Role) Can(c Capability) bool {
	if r == RoleSystemAdmin {
		return true
	}
	return capabilityRoles[c][r]
}

type User struct {
	ID           int64  `gorm:"primaryKey" json:"id"`
	Username     string `gorm:"uniqueIndex;size:190;not null" json:"username"`
	Email        string `gorm:"not null;default:''" json:"email"`
	PasswordHash string `gorm:"not null;default:''" json:"-"`
	Role         Role   `gorm:"not null" json:"role"`
	Source       string `gorm:"not null;default:db" json:"source"` // db | static | ldap
	// API token auth (currently only used by RoleAgent - DESIGN/08 §8.1):
	// a static long-lived token, split into an indexed public ID and a hashed
	// secret so lookup doesn't require iterating every user. This is
	// deliberately a narrow, swappable seam - see auth.IssueAPIToken - not a
	// general auth framework, so a future OIDC/external-IdP login for Agent
	// can replace it without touching anything else (same extension-point
	// pattern as the documented-but-unwired LDAP_ENABLED config today).
	APITokenID   *string   `gorm:"uniqueIndex;size:32" json:"-"`
	APITokenHash *string   `gorm:"size:64" json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Organization is the multi-tenant boundary: Customers log in with an org
// name plus username/password and only see tickets scoped to their org (own
// tickets, plus tickets they've been added to watch within that org).
// Engineer/Manager/SystemAdmin are not org-scoped - they see everything.
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
	// SLATargets is a JSON blob mapping Priority -> SLA duration in minutes
	// (e.g. {"P1":240,"P2":480}), mirroring the Ticket.CustomFields pattern
	// (DESIGN/08 §8.6). Empty/missing priorities fall back to package-level
	// defaults - see service.slaTargetsFor.
	SLATargets string `gorm:"not null;default:'{}'" json:"sla_targets"`
}

// QueueMembership is which Engineers belong to which queue (e.g. an
// "Engineers" or "Networking" queue) - pickup/assign is restricted to
// members of a ticket's queue (Manager, or SystemAdmin via Sudo-as, bypass this).
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
	// ServiceID is the business service this ticket impacts (RELEASE/v_2.1.0.md's
	// Service catalog) - optional and never required, "unknown" is a normal
	// state. Orthogonal to QueueID: Queue is which team routes/handles the
	// ticket, Service is which business-facing system it's about.
	ServiceID *int64 `gorm:"index" json:"service_id"`
	// OrgID scopes Customer visibility (multi-tenant); 0 for tickets created
	// by internal staff, who aren't tied to any organization.
	OrgID        int64      `gorm:"not null;default:0;index" json:"org_id"`
	CustomFields string     `gorm:"not null;default:'{}'" json:"custom_fields"` // JSON blob
	SLADueAt     *time.Time `json:"sla_due_at"`
	// SLABreachNotifiedAt marks that the ticket.sla_breached webhook has
	// already fired for the current SLADueAt (RELEASE/v_2.0.0.md) - the
	// background poller (service.SLABreachChecker) claims a ticket by
	// stamping this, so it only ever alerts once per breach. Cleared on
	// reopen (alongside ResolvedAt) so a ticket that breaches again after
	// being reopened alerts again.
	SLABreachNotifiedAt *time.Time `json:"sla_breach_notified_at"`

	// Stage-tracking overlay (DESIGN/03 §3.1.2b): Detect -> Ack -> Mitigate ->
	// Resolve, driving the shared Ticket Progress Bar (DESIGN/08 §8.2). This
	// is purely additive display/metrics data - it never gates or replaces a
	// Status transition above, and Rejected sits outside it entirely.
	DetectedAt  *time.Time `json:"detected_at"`  // defaults to CreatedAt; Agent can backdate (CapAgentDetect)
	AckedAt     *time.Time `json:"acked_at"`     // stamped once on first pickup/assign, never overwritten
	MitigatedAt *time.Time `json:"mitigated_at"` // stamped by MarkMitigated; overwritten if mitigated again after a reopen
	ResolvedAt  *time.Time `json:"resolved_at"`  // stamped on resolve; cleared on reopen, re-stamped if resolved again
	ReopenCount int        `gorm:"not null;default:0" json:"reopen_count"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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

// Attachment (DESIGN/08 §8.7): file uploads on a ticket or a specific note.
// TicketID is always set (denormalized, even when NoteID is too) so "all
// attachments for this ticket" is a single indexed query, not a join through
// notes. NoteID nil means a ticket-level attachment (e.g. the submission
// form); non-nil means it belongs to that specific note and Internal is
// inherited from it - an attachment on an internal note must never reach the
// Customer view.
//
// Visibility has one more wrinkle beyond Internal: a Customer-uploaded
// attachment is private to that uploader among other Customers, even ones
// who can otherwise see the same ticket (e.g. a same-org coworker added as a
// watcher) - CustomerPrivate + UploaderID encode that. Staff (Engineer/
// Manager/SystemAdmin/Agent) always see every attachment regardless of who
// uploaded it; a staff-uploaded attachment on an external note is visible to
// any Customer who can see the ticket, same as before. See
// service.AttachmentService.CanView for the single place this is decided.
type Attachment struct {
	ID         int64  `gorm:"primaryKey" json:"id"`
	TicketID   int64  `gorm:"not null;index" json:"ticket_id"`
	NoteID     *int64 `gorm:"index" json:"note_id"`
	UploaderID int64  `gorm:"not null" json:"uploader_id"`
	Filename   string `gorm:"not null" json:"filename"`
	MIMEType   string `gorm:"not null" json:"mime_type"`
	SizeBytes  int64  `gorm:"not null" json:"size_bytes"`
	Internal   bool   `gorm:"not null;default:false" json:"internal"`
	// CustomerPrivate is set at upload time from whether the uploader was a
	// Customer - true means only UploaderID may view it among Customer
	// viewers (staff are unaffected by this flag, they always see it).
	CustomerPrivate bool `gorm:"not null;default:false" json:"customer_private"`
	// StorageBackend is a discriminator for where Data actually lives - "db"
	// today (the bytes are this row's Data column); a future backend value
	// (e.g. "rustfs"/"s3") would mean Data is nil and the bytes live
	// elsewhere, without needing a schema change to introduce it.
	StorageBackend string    `gorm:"not null;default:db;size:16" json:"storage_backend"`
	Data           []byte    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

// TicketAISnapshot is one versioned generation of the AI Ticket Intelligence
// Panel (DESIGN/08 §8.9): every regeneration (triggered by a new note) and
// every human edit is stored as its own row, even though only the latest is
// shown - this is what turns raw generations into a (ticket history so far ->
// structured extraction) dataset useful for evaluating/fine-tuning later.
// Fields is a JSON map[string]string keyed by the panel's field names
// (service.summaryFieldOrder); EditedFields is a JSON []string of field names
// an Engineer has locked, which the next AI regeneration must leave alone.
type TicketAISnapshot struct {
	ID               int64  `gorm:"primaryKey" json:"id"`
	TicketID         int64  `gorm:"not null;index" json:"ticket_id"`
	TriggeringNoteID *int64 `json:"triggering_note_id"`
	// Source distinguishes an AI-generated snapshot from a human correction -
	// the latter is closer to a gold label than a raw model guess.
	Source       string    `gorm:"not null;size:16" json:"source"` // "ai" | "human_edit"
	Fields       string    `gorm:"not null;default:'{}'" json:"fields"`
	EditedFields string    `gorm:"not null;default:'[]'" json:"edited_fields"`
	GeneratedAt  time.Time `gorm:"autoCreateTime" json:"generated_at"`
}

type Watcher struct {
	TicketID int64 `gorm:"primaryKey" json:"ticket_id"`
	UserID   int64 `gorm:"primaryKey" json:"user_id"`
}

type ServiceCriticality string

const (
	ServiceCriticalityCritical ServiceCriticality = "Critical"
	ServiceCriticalityHigh     ServiceCriticality = "High"
	ServiceCriticalityMedium   ServiceCriticality = "Medium"
	ServiceCriticalityLow      ServiceCriticality = "Low"
)

type ServiceStatus string

const (
	ServiceStatusActive     ServiceStatus = "active"
	ServiceStatusDeprecated ServiceStatus = "deprecated"
	ServiceStatusRetired    ServiceStatus = "retired"
)

// Service is the business-service catalog (RELEASE/v_2.1.0.md), e.g. "Mail
// Service" or "Payment Gateway" - a CMDB-lite concept distinct from Queue:
// Queue is which team routes/handles a ticket, Service is which
// business-facing system it's about. Criticality/Status/Owner/SupportQueue
// are the common fields research on CMDB business-service models converges
// on (ServiceNow business-criticality tiers, ITIL known-error "affected
// service"). CRUD is SystemAdmin-only (see httpapi/service_handlers.go) -
// a system-configuration concern like Users/Webhooks/Workflows, not a
// day-to-day queue/SLA concern like Queue/CustomFieldDef.
type Service struct {
	ID          int64              `gorm:"primaryKey" json:"id"`
	Name        string             `gorm:"not null;size:190;uniqueIndex" json:"name"`
	Description string             `gorm:"not null;default:''" json:"description"`
	Criticality ServiceCriticality `gorm:"not null;default:Medium;size:16" json:"criticality"`
	Status      ServiceStatus      `gorm:"not null;default:active;size:16" json:"status"`
	OwnerID     *int64             `gorm:"index" json:"owner_id"`
	// SupportQueueID is which Queue picks up incidents against this service -
	// optional, since not every service maps 1:1 onto a queue.
	SupportQueueID *int64 `gorm:"index" json:"support_queue_id"`
	// ParentID makes this self-referencing (same pattern as
	// Organization.ParentID / Queue.ParentID) so a component service (e.g.
	// "Exchange Online") can roll up into a parent business service (e.g.
	// "Mail Service") without a schema change later.
	ParentID *int64 `json:"parent_id"`
	// External CMDB sync seam (schema-only this cycle, RELEASE/v_2.1.0.md) -
	// same StorageBackend-style extension point as Attachment: empty means
	// this row is the source of truth; a future sync job would populate these
	// against whatever external CMDB/service-catalog tool a deployment uses.
	ExternalSource string    `gorm:"not null;default:'';size:32" json:"external_source"`
	ExternalID     string    `gorm:"not null;default:'';size:190" json:"external_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type KBArticleStatus string

const (
	KBStatusDraft     KBArticleStatus = "draft"
	KBStatusPublished KBArticleStatus = "published"
)

// KBArticle is the Knowledge Base Feedback Loop's article (DESIGN/08 §8.10):
// every resolved ticket becomes a candidate contribution, curated by a human
// before anything reaches a Customer-facing surface. Field list is grounded
// in KCS methodology (Issue/Environment/Cause/Resolution) and ITIL
// known-error-database conventions (symptom, affected service, root cause,
// workaround, resolution), split into a customer-facing block (surfaced
// as-is once published) and an engineer/internal-only block (never shown to
// a Customer, regardless of publish status).
type KBArticle struct {
	ID     int64           `gorm:"primaryKey" json:"id"`
	Title  string          `gorm:"not null" json:"title"`
	Status KBArticleStatus `gorm:"not null;default:draft;size:16;index" json:"status"`

	// Customer-facing fields (DESIGN/08 §8.10 point 3) - only ever reach a
	// Customer once Status is published (KBService.CanView).
	Symptom          string `gorm:"not null;default:''" json:"symptom"`            // KCS "Issue" - the customer's own words
	WhatToObserve    string `gorm:"not null;default:''" json:"what_to_observe"`    // recognizable signs, phrased for a future customer
	SelfServiceSteps string `gorm:"not null;default:''" json:"self_service_steps"` // maintained checklist a customer can try before filing
	Resolution       string `gorm:"not null;default:''" json:"resolution"`         // customer-facing fix/workaround copy

	// Engineer/internal-only fields - never reach a Customer-facing surface.
	Environment     string `gorm:"not null;default:''" json:"environment"` // KCS "Environment": product/version/process this applies to
	RootCause       string `gorm:"not null;default:''" json:"root_cause"`
	ValidationSteps string `gorm:"not null;default:''" json:"validation_steps"` // how the root cause was confirmed, not just what it was
	ResolutionSteps string `gorm:"not null;default:''" json:"resolution_steps"` // the actual technical fix procedure (vs. the customer-facing Resolution copy)
	Workaround      string `gorm:"not null;default:''" json:"workaround"`       // temporary mitigation, distinct from the permanent Resolution
	// BlastRadius is the scope of impact for the incident this article
	// documents - which users/transactions/services were pulled in
	// (structural + execution blast radius, per incident-response
	// convention). Which specific Service(s) were impacted is captured via
	// KBArticleService below, not duplicated here as free text.
	BlastRadius string `gorm:"not null;default:''" json:"blast_radius"`

	// Lifecycle / provenance
	SourceTicketID   *int64     `gorm:"index" json:"source_ticket_id"`
	SourceSnapshotID *int64     `json:"source_snapshot_id"` // the TicketAISnapshot this was proposed from, if any
	RelatedArticleID *int64     `json:"related_article_id"` // set when proposed as a diff against an existing similar article (KBService.MatchForSymptom)
	CreatedByID      int64      `gorm:"not null" json:"created_by_id"`
	ApprovedByID     *int64     `json:"approved_by_id"`
	PublishedAt      *time.Time `json:"published_at"`

	// External sync seam (schema-only this cycle, same pattern as Service above).
	ExternalSource string     `gorm:"not null;default:'';size:32" json:"external_source"`
	ExternalID     string     `gorm:"not null;default:'';size:190" json:"external_id"`
	ExternalURL    string     `gorm:"not null;default:''" json:"external_url"`
	LastSyncedAt   *time.Time `json:"last_synced_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// KBArticleService links an article to the business Service(s) it covers -
// many-to-many, since one incident (and its article) can span multiple
// services (e.g. an SSO outage touching both Email and Payments).
// Criticality is deliberately read live off the linked Service rather than
// copied onto KBArticle, so it can't drift from the catalog.
type KBArticleService struct {
	KBArticleID int64 `gorm:"primaryKey" json:"kb_article_id"`
	ServiceID   int64 `gorm:"primaryKey" json:"service_id"`
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
	ID       int64  `gorm:"primaryKey" json:"id"`
	TicketID *int64 `gorm:"index" json:"ticket_id"`
	ActorID  *int64 `gorm:"index" json:"actor_id"`
	Event    string `gorm:"not null" json:"event"`
	Details  string `gorm:"not null;default:'{}'" json:"details"` // JSON
	// SudoByID records the real admin's user ID when this event happened
	// during a Sudo-as session (ActorID is always the sudo target's, so
	// every other RBAC/attribution path works unmodified) - DESIGN/02 §2.5.
	SudoByID  *int64    `gorm:"index" json:"sudo_by_id"`
	CreatedAt time.Time `json:"created_at"`
}
