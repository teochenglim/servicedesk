package httpapi

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gorm.io/gorm"

	"servicedesk/internal/auth"
	"servicedesk/internal/metrics"
	"servicedesk/internal/middleware"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
	"servicedesk/internal/service"
	"servicedesk/internal/sse"
	"servicedesk/internal/workflow"
	"servicedesk/web"
)

type Server struct {
	render  *Renderer
	authMgr *auth.Manager
	log     *slog.Logger
	db      *gorm.DB

	users        *repo.UserRepo
	orgs         *repo.OrgRepo
	orgMembers   *repo.OrgMembershipRepo
	queues       *repo.QueueRepo
	queueMembers *repo.QueueMembershipRepo
	tags         *repo.TagRepo
	watchers     *repo.WatcherRepo
	webhooks     *repo.WebhookRepo
	workflows    *repo.WorkflowRepo
	workflowTask *repo.WorkflowTaskRepo
	approvals    *repo.ApprovalRepo
	customFields *repo.CustomFieldRepo
	events       *repo.EventLogRepo
	// tickets is a direct read-only repo handle (unlike ticketSvc below, which
	// owns the state machine/mutations) - used for read-mostly queries with no
	// business logic, e.g. the Manager dashboard's MTTx trend chart
	// (RELEASE/v_3.0.0.md), matching how queues/users are already handled.
	tickets *repo.TicketRepo

	ticketSvc     *service.TicketService
	noteSvc       *service.NoteService
	problemSvc    *service.ProblemService
	attachmentSvc *service.AttachmentService
	queueSvc      *service.QueueService
	sudoSvc       *service.SudoService
	serviceSvc    *service.ServiceCatalogService
	categorySvc   *service.CategoryService
	kbSvc         *service.KBService
	aiSummarySvc  *service.AISummaryService // nil unless aiEnabled
	aiDraftSvc    *service.AIDraftService   // nil unless aiEnabled

	engine *workflow.Engine
	hub    *sse.Hub

	demoMode  bool
	aiEnabled bool
}

func NewServer(
	authMgr *auth.Manager, log *slog.Logger,
	users *repo.UserRepo, orgs *repo.OrgRepo, orgMembers *repo.OrgMembershipRepo,
	queues *repo.QueueRepo, queueMembers *repo.QueueMembershipRepo,
	tags *repo.TagRepo, watchers *repo.WatcherRepo,
	webhooks *repo.WebhookRepo, workflows *repo.WorkflowRepo, workflowTask *repo.WorkflowTaskRepo,
	approvals *repo.ApprovalRepo, customFields *repo.CustomFieldRepo, events *repo.EventLogRepo, tickets *repo.TicketRepo,
	ticketSvc *service.TicketService, noteSvc *service.NoteService, problemSvc *service.ProblemService,
	attachmentSvc *service.AttachmentService, queueSvc *service.QueueService, sudoSvc *service.SudoService,
	serviceSvc *service.ServiceCatalogService, categorySvc *service.CategoryService, kbSvc *service.KBService,
	aiSummarySvc *service.AISummaryService, aiDraftSvc *service.AIDraftService, aiEnabled bool,
	engine *workflow.Engine, hub *sse.Hub,
) *Server {
	return &Server{
		render: NewRenderer(), authMgr: authMgr, log: log,
		users: users, orgs: orgs, orgMembers: orgMembers,
		queues: queues, queueMembers: queueMembers, tags: tags, watchers: watchers,
		webhooks: webhooks, workflows: workflows, workflowTask: workflowTask,
		approvals: approvals, customFields: customFields, events: events, tickets: tickets,
		ticketSvc: ticketSvc, noteSvc: noteSvc, problemSvc: problemSvc, attachmentSvc: attachmentSvc,
		queueSvc: queueSvc, sudoSvc: sudoSvc, serviceSvc: serviceSvc, categorySvc: categorySvc, kbSvc: kbSvc,
		aiSummarySvc: aiSummarySvc, aiDraftSvc: aiDraftSvc, aiEnabled: aiEnabled,
		engine: engine, hub: hub,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	auth_ := middleware.RequireAuth(s.authMgr, s.users.GetByAPITokenID)
	protect := func(h http.HandlerFunc) http.Handler { return auth_(h) }
	agentOnly := func(h http.HandlerFunc) http.Handler {
		return auth_(middleware.RequireRole(models.RoleEngineer)(h))
	}
	adminOnly := func(h http.HandlerFunc) http.Handler {
		return auth_(middleware.RequireRole(models.RoleSystemAdmin)(h))
	}
	queueAdminOnly := func(h http.HandlerFunc) http.Handler {
		return auth_(middleware.RequireCapability(models.CapQueueOps)(h))
	}
	sudoOnly := func(h http.HandlerFunc) http.Handler {
		return auth_(middleware.RequireCapability(models.CapSudo)(h))
	}

	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.Handle("POST /logout", protect(s.handleLogout))

	mux.Handle("GET /tickets", protect(s.handleTicketsList))
	mux.Handle("GET /tickets/new", protect(s.handleTicketNewPage))
	// Dynamic custom fields on the ticket-create form (RELEASE/v_3.0.0.md) -
	// any authenticated user, matches ticket creation itself.
	mux.Handle("GET /custom-fields/for-category", protect(s.handleCustomFieldsForCategory))
	mux.Handle("POST /tickets", protect(s.handleTicketCreate))
	// Submission-time KB suggestion popup (DESIGN/08 §8.10, RELEASE/v_3.0.0.md) -
	// not gated behind aiEnabled: KBService works independent of AI features.
	mux.Handle("POST /tickets/match-symptom", protect(s.handleKBMatchSymptom))
	mux.Handle("GET /tickets/{id}", protect(s.handleTicketDetail))
	mux.Handle("POST /tickets/{id}/transition", protect(s.handleTicketTransition))
	// Pickup is self-assign (any Engineer+); Assign/transfer to another
	// engineer requires CapQueueOps (DESIGN/02 §2.1.1: Manager owns queues/assignment).
	mux.Handle("POST /tickets/{id}/pickup", agentOnly(s.handleTicketPickup))
	mux.Handle("POST /tickets/{id}/assign", queueAdminOnly(s.handleTicketAssign))
	// Mark-mitigated is an Engineer/Agent action (DESIGN/03 §3.1.2b) - stamps
	// the stage overlay only, doesn't touch Status, so it shares agentOnly's
	// gate rather than needing a new capability.
	mux.Handle("POST /tickets/{id}/mitigate", agentOnly(s.handleTicketMitigate))
	mux.Handle("POST /tickets/{id}/notes", protect(s.handleNoteCreate))
	// Attachments (DESIGN/08 §8.7): any authenticated user can upload to a
	// ticket they can already see (Customer visibility is re-checked inside
	// the handler, same as handleTicketDetail); note-scoped uploads inherit
	// that note's internal/external visibility. Download is `protect`-gated
	// too - the real visibility decision is service.CanView, not the route.
	mux.Handle("POST /tickets/{id}/attachments", protect(s.handleAttachmentUpload))
	mux.Handle("GET /attachments/{id}", protect(s.handleAttachmentDownload))
	mux.Handle("POST /tickets/{id}/watch", protect(s.handleWatch))
	mux.Handle("POST /tickets/{id}/watchers", protect(s.handleWatchersAdd))
	mux.Handle("POST /tickets/{id}/unwatch", protect(s.handleUnwatch))
	// Setting/correcting the impacted service is a triage action (RELEASE/v_2.1.0.md).
	mux.Handle("POST /tickets/{id}/service", agentOnly(s.handleTicketServiceUpdate))
	mux.Handle("POST /tickets/{id}/labels", agentOnly(s.handleLabelAdd))
	mux.Handle("POST /tickets/{id}/labels/{tagID}/delete", agentOnly(s.handleLabelRemove))
	mux.Handle("POST /tickets/{id}/runbooks/{workflowID}/start", agentOnly(s.handleRunbookStart))
	mux.Handle("POST /workflow-tasks/{id}/resume", agentOnly(s.handleWorkflowResume))
	mux.Handle("POST /approvals/{id}/decide", agentOnly(s.handleApprovalDecide))

	// AI-assisted drafting + AI Ticket Intelligence Panel (DESIGN/08 §8.8-8.9) -
	// only registered at all when enabled (like the demo-reset route below),
	// so they 404 rather than error when no LLM is configured.
	if s.aiEnabled {
		// draft-description has no ticket ID yet (ticket submission) - any
		// authenticated user, since Customers use it too (DESIGN/08 §8.8).
		mux.Handle("POST /tickets/draft-description", protect(s.handleAIDraftDescription))
		// Resolution/transfer drafts and the Intelligence Panel are Engineer-facing.
		mux.Handle("POST /tickets/{id}/ai-draft", agentOnly(s.handleAIDraft))
		mux.Handle("POST /tickets/{id}/ai-summary/regenerate", agentOnly(s.handleAISummaryRegenerate))
		mux.Handle("POST /tickets/{id}/ai-summary/{field}/edit", agentOnly(s.handleAISummaryEditField))
		mux.Handle("POST /tickets/{id}/ai-summary/{field}/regenerate", agentOnly(s.handleAISummaryRegenerateField))
	}

	// Queue is an internal routing concept (DESIGN/08 §8.4/§8.6) - Customer
	// never needs to know which queue their ticket is in.
	mux.Handle("GET /queues", agentOnly(s.handleQueuesList))
	mux.Handle("POST /queues", queueAdminOnly(s.handleQueueCreate))
	mux.Handle("POST /queues/{id}/delete", queueAdminOnly(s.handleQueueDelete))
	mux.Handle("POST /queues/{id}/members", queueAdminOnly(s.handleQueueMemberAdd))
	mux.Handle("POST /queues/{id}/members/{userID}/remove", queueAdminOnly(s.handleQueueMemberRemove))
	mux.Handle("POST /queues/{id}/sla", queueAdminOnly(s.handleQueueSLAUpdate))

	mux.Handle("GET /manager", queueAdminOnly(s.handleManagerDashboard))
	mux.Handle("GET /manager/activity", queueAdminOnly(s.handleManagerActivity))

	// Problem Management/RCA is an Engineer+ concept - not a Customer concern
	// (DESIGN/08 §8.4 covers only Tickets/KB for that persona).
	mux.Handle("GET /problems", agentOnly(s.handleProblemsList))
	mux.Handle("POST /problems", agentOnly(s.handleProblemCreate))
	mux.Handle("GET /problems/{id}", agentOnly(s.handleProblemDetail))
	mux.Handle("POST /problems/{id}/link", agentOnly(s.handleProblemLink))

	// Knowledge Base Feedback Loop (DESIGN/08 §8.10): /kb is the published,
	// customer-safe browse surface (any authenticated role); /kb/review is
	// the human curation queue (Engineer+ only) that gates anything ever
	// reaching /kb. Submission-time/triage-time suggestion popups are a
	// deferred follow-up - see RELEASE/v_2.1.0.md.
	mux.Handle("GET /kb", protect(s.handleKBList))
	mux.Handle("GET /kb/review", agentOnly(s.handleKBReview))
	mux.Handle("GET /kb/{id}", protect(s.handleKBDetail))
	mux.Handle("POST /kb/{id}", agentOnly(s.handleKBUpdate))
	mux.Handle("POST /kb/{id}/approve", agentOnly(s.handleKBApprove))
	mux.Handle("POST /kb/{id}/delete", agentOnly(s.handleKBDelete))

	// GET /admin is now ServiceDeskAdmin's own home screen (full user list +
	// Sudo-as buttons, DESIGN/08 §8.3) - adminOnly, not agentOnly, since that
	// content (every user's role, one-tap sudo) must not be visible to
	// Engineer/Manager/Agent just because they're staff.
	mux.Handle("GET /admin", adminOnly(s.handleAdminIndex))
	mux.Handle("GET /admin/orgs", adminOnly(s.handleOrgsList))
	mux.Handle("POST /admin/orgs", adminOnly(s.handleOrgCreate))
	mux.Handle("POST /admin/orgs/{id}/members", adminOnly(s.handleOrgMemberAdd))
	mux.Handle("POST /admin/orgs/{id}/members/{userID}/remove", adminOnly(s.handleOrgMemberRemove))
	mux.Handle("GET /admin/webhooks", adminOnly(s.handleWebhooksList))
	mux.Handle("POST /admin/webhooks", adminOnly(s.handleWebhookCreate))
	mux.Handle("POST /admin/webhooks/{id}/delete", adminOnly(s.handleWebhookDelete))
	mux.Handle("GET /admin/workflows", adminOnly(s.handleWorkflowsList))
	mux.Handle("POST /admin/workflows", adminOnly(s.handleWorkflowCreate))
	mux.Handle("GET /admin/users", adminOnly(s.handleUsersList))
	mux.Handle("POST /admin/users", adminOnly(s.handleUserCreate))
	mux.Handle("POST /admin/users/{id}/role", adminOnly(s.handleUserRoleUpdate))
	mux.Handle("POST /admin/users/{id}/api-token", adminOnly(s.handleUserIssueAPIToken))
	// Custom field definitions (RELEASE/v_3.0.0.md) - SystemAdmin-only, same
	// gate as Users/Webhooks/Workflows: defining the schema of what data gets
	// collected is a system-configuration concern, distinct from Queue's
	// day-to-day routing/SLA config (which stays Manager-owned via CapQueueOps).
	mux.Handle("GET /admin/custom-fields", adminOnly(s.handleCustomFieldsList))
	mux.Handle("POST /admin/custom-fields", adminOnly(s.handleCustomFieldCreate))
	// Service catalog (RELEASE/v_2.1.0.md) - SystemAdmin-only, same reasoning.
	mux.Handle("GET /admin/services", adminOnly(s.handleServicesList))
	mux.Handle("POST /admin/services", adminOnly(s.handleServiceCreate))
	mux.Handle("POST /admin/services/{id}", adminOnly(s.handleServiceUpdate))
	mux.Handle("POST /admin/services/{id}/delete", adminOnly(s.handleServiceDelete))
	// Category catalog (RELEASE/v_3.0.5.md) - SystemAdmin-only, same reasoning
	// as the Service catalog/Custom Fields above.
	mux.Handle("GET /admin/categories", adminOnly(s.handleCategoriesList))
	mux.Handle("POST /admin/categories", adminOnly(s.handleCategoryCreate))
	mux.Handle("POST /admin/categories/{id}", adminOnly(s.handleCategoryUpdate))
	mux.Handle("POST /admin/categories/{id}/delete", adminOnly(s.handleCategoryDelete))
	// Sudo-as (DESIGN/02 §2.5): Start requires CapSudo (SystemAdmin only,
	// checked again inside SudoService.Start per ARCHITECTURE.md's
	// defense-in-depth rule). Stop is intentionally just `protect` - while a
	// sudo session is active, claims reflect the *target's* role/capabilities,
	// which could be anything, so only SudoService.Stop's SudoByID check gates it.
	mux.Handle("POST /admin/users/{id}/sudo/start", sudoOnly(s.handleSudoStart))
	mux.Handle("POST /admin/sudo/stop", protect(s.handleSudoStop))
	mux.Handle("GET /admin/audit", adminOnly(s.handleAuditLog))

	// Only registered at all in demo mode, so it can't be discovered or hit on
	// a real deployment (see RELEASE/v_1.0.8.md).
	if s.demoMode {
		mux.Handle("POST /admin/demo/reset", adminOnly(s.handleDemoReset))
	}

	mux.Handle("GET /events", protect(s.hub.Handler))
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.Handle("GET /metrics", promhttp.Handler())

	staticFS, _ := fs.Sub(web.StaticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	// Landing dispatch (DESIGN/08 §8.3/§8.6): a bare SystemAdmin session lands
	// on the short admin home; Manager (or SystemAdmin acting as one via an
	// active Sudo-as session - Role reflects the sudo target, so this check
	// naturally applies during sudo too) lands on the dashboard; every other
	// persona lands on the shared ticket workspace.
	mux.Handle("GET /", protect(func(w http.ResponseWriter, r *http.Request) {
		claims := middleware.ClaimsFrom(r.Context())
		switch {
		case claims == nil:
			http.Redirect(w, r, "/tickets", http.StatusSeeOther)
		case claims.Role == models.RoleSystemAdmin:
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
		case claims.Role.Can(models.CapQueueOps):
			http.Redirect(w, r, "/manager", http.StatusSeeOther)
		default:
			http.Redirect(w, r, "/tickets", http.StatusSeeOther)
		}
	}))

	return metrics.Middleware(mux)
}
