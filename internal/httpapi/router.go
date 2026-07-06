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

	ticketSvc  *service.TicketService
	noteSvc    *service.NoteService
	problemSvc *service.ProblemService

	engine *workflow.Engine
	hub    *sse.Hub
}

func NewServer(
	authMgr *auth.Manager, log *slog.Logger,
	users *repo.UserRepo, orgs *repo.OrgRepo, orgMembers *repo.OrgMembershipRepo,
	queues *repo.QueueRepo, queueMembers *repo.QueueMembershipRepo,
	tags *repo.TagRepo, watchers *repo.WatcherRepo,
	webhooks *repo.WebhookRepo, workflows *repo.WorkflowRepo, workflowTask *repo.WorkflowTaskRepo,
	approvals *repo.ApprovalRepo, customFields *repo.CustomFieldRepo, events *repo.EventLogRepo,
	ticketSvc *service.TicketService, noteSvc *service.NoteService, problemSvc *service.ProblemService,
	engine *workflow.Engine, hub *sse.Hub,
) *Server {
	return &Server{
		render: NewRenderer(), authMgr: authMgr, log: log,
		users: users, orgs: orgs, orgMembers: orgMembers,
		queues: queues, queueMembers: queueMembers, tags: tags, watchers: watchers,
		webhooks: webhooks, workflows: workflows, workflowTask: workflowTask,
		approvals: approvals, customFields: customFields, events: events,
		ticketSvc: ticketSvc, noteSvc: noteSvc, problemSvc: problemSvc,
		engine: engine, hub: hub,
	}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	auth_ := middleware.RequireAuth(s.authMgr)
	protect := func(h http.HandlerFunc) http.Handler { return auth_(h) }
	agentOnly := func(h http.HandlerFunc) http.Handler {
		return auth_(middleware.RequireRole(models.RoleEngineer)(h))
	}
	adminOnly := func(h http.HandlerFunc) http.Handler {
		return auth_(middleware.RequireRole(models.RoleSystemAdmin)(h))
	}
	queueAdminOnly := func(h http.HandlerFunc) http.Handler {
		return auth_(middleware.RequireRole(models.RoleQueueAdmin)(h))
	}

	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.Handle("POST /logout", protect(s.handleLogout))

	mux.Handle("GET /tickets", protect(s.handleTicketsList))
	mux.Handle("GET /tickets/new", protect(s.handleTicketNewPage))
	mux.Handle("POST /tickets", protect(s.handleTicketCreate))
	mux.Handle("GET /tickets/{id}", protect(s.handleTicketDetail))
	mux.Handle("POST /tickets/{id}/transition", protect(s.handleTicketTransition))
	// Pickup is self-assign (any Engineer+); Assign/transfer to another
	// engineer is a QueueAdmin+ action (DESIGN.md 2: Queue Admin manages queues/assignment).
	mux.Handle("POST /tickets/{id}/pickup", agentOnly(s.handleTicketPickup))
	mux.Handle("POST /tickets/{id}/assign", queueAdminOnly(s.handleTicketAssign))
	mux.Handle("POST /tickets/{id}/notes", protect(s.handleNoteCreate))
	mux.Handle("POST /tickets/{id}/watch", protect(s.handleWatch))
	mux.Handle("POST /tickets/{id}/unwatch", protect(s.handleUnwatch))
	mux.Handle("POST /tickets/{id}/labels", agentOnly(s.handleLabelAdd))
	mux.Handle("POST /tickets/{id}/labels/{tagID}/delete", agentOnly(s.handleLabelRemove))
	mux.Handle("POST /tickets/{id}/runbooks/{workflowID}/start", agentOnly(s.handleRunbookStart))
	mux.Handle("POST /workflow-tasks/{id}/resume", agentOnly(s.handleWorkflowResume))
	mux.Handle("POST /approvals/{id}/decide", agentOnly(s.handleApprovalDecide))

	mux.Handle("GET /queues", protect(s.handleQueuesList))
	mux.Handle("POST /queues", queueAdminOnly(s.handleQueueCreate))
	mux.Handle("POST /queues/{id}/delete", queueAdminOnly(s.handleQueueDelete))
	mux.Handle("POST /queues/{id}/members", queueAdminOnly(s.handleQueueMemberAdd))
	mux.Handle("POST /queues/{id}/members/{userID}/remove", queueAdminOnly(s.handleQueueMemberRemove))

	mux.Handle("GET /problems", protect(s.handleProblemsList))
	mux.Handle("POST /problems", agentOnly(s.handleProblemCreate))
	mux.Handle("GET /problems/{id}", protect(s.handleProblemDetail))
	mux.Handle("POST /problems/{id}/link", agentOnly(s.handleProblemLink))

	mux.Handle("GET /admin", agentOnly(s.handleAdminIndex))
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
	mux.Handle("GET /admin/custom-fields", queueAdminOnly(s.handleCustomFieldsList))
	mux.Handle("POST /admin/custom-fields", queueAdminOnly(s.handleCustomFieldCreate))

	mux.Handle("GET /events", protect(s.hub.Handler))
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.Handle("GET /metrics", promhttp.Handler())

	staticFS, _ := fs.Sub(web.StaticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/tickets", http.StatusSeeOther)
	})

	return metrics.Middleware(mux)
}
