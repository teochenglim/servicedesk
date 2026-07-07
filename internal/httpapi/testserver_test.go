package httpapi

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"gorm.io/gorm"

	"servicedesk/internal/auth"
	"servicedesk/internal/config"
	"servicedesk/internal/db"
	"servicedesk/internal/llm"
	"servicedesk/internal/logging"
	"servicedesk/internal/mailer"
	"servicedesk/internal/repo"
	"servicedesk/internal/service"
	"servicedesk/internal/sse"
	"servicedesk/internal/webhook"
	"servicedesk/internal/workflow"
)

// testEnv wires the exact same dependency graph as cmd/servicedesk/main.go
// against a private in-memory sqlite DB, so integration tests exercise the
// real routes/services/repos/GORM layer end to end (DESIGN.md functional requirements).
type testEnv struct {
	t       *testing.T
	db      *gorm.DB
	server  *Server
	http    *httptest.Server
	authMgr *auth.Manager

	users        *repo.UserRepo
	orgs         *repo.OrgRepo
	orgMembers   *repo.OrgMembershipRepo
	queues       *repo.QueueRepo
	queueMembers *repo.QueueMembershipRepo
	webhooks     *repo.WebhookRepo
	workflows    *repo.WorkflowRepo
	workflowTask *repo.WorkflowTaskRepo
	approvals    *repo.ApprovalRepo
	services     *repo.ServiceRepo
	kbArticles   *repo.KBArticleRepo
	tickets      *repo.TicketRepo

	engine           *workflow.Engine
	whDispatcher     *webhook.Dispatcher
	slaBreachChecker *service.SLABreachChecker
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	return newTestEnvWithAI(t, nil)
}

// newTestEnvWithAI wires the same dependency graph as newTestEnv, but with
// AI-assisted drafting + the Intelligence Panel turned on when aiClient is
// non-nil (typically an *llm.FakeClient the caller keeps a reference to for
// assertions) - nil leaves AI disabled, matching production's default.
func newTestEnvWithAI(t *testing.T, aiClient llm.Client) *testEnv {
	t.Helper()

	gdb, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := gdb.DB()
		sqlDB.Close()
	})

	log := logging.New("error") // keep test output quiet

	users := repo.NewUserRepo(gdb)
	orgs := repo.NewOrgRepo(gdb)
	orgMembers := repo.NewOrgMembershipRepo(gdb)
	queues := repo.NewQueueRepo(gdb)
	queueMembers := repo.NewQueueMembershipRepo(gdb)
	tickets := repo.NewTicketRepo(gdb)
	notes := repo.NewNoteRepo(gdb)
	tags := repo.NewTagRepo(gdb)
	watchers := repo.NewWatcherRepo(gdb)
	problems := repo.NewProblemRepo(gdb)
	webhooks := repo.NewWebhookRepo(gdb)
	webhookDeliveries := repo.NewWebhookDeliveryRepo(gdb)
	workflows := repo.NewWorkflowRepo(gdb)
	workflowTask := repo.NewWorkflowTaskRepo(gdb)
	approvals := repo.NewApprovalRepo(gdb)
	customFields := repo.NewCustomFieldRepo(gdb)
	events := repo.NewEventLogRepo(gdb)
	attachments := repo.NewAttachmentRepo(gdb)
	services := repo.NewServiceRepo(gdb)
	kbArticles := repo.NewKBArticleRepo(gdb)

	cfg := config.Config{StaticUsers: ""}
	if err := auth.Bootstrap(users, cfg, log); err != nil {
		t.Fatalf("auth.Bootstrap: %v", err)
	}
	authMgr := auth.NewManager("test-secret", "servicedesk-test")

	hub := sse.NewHub(watchers, tickets)
	whDispatcher := webhook.NewDispatcher(webhooks, webhookDeliveries, log)
	mail := mailer.New("", 0, "test@example.com", "", "", log)
	engine := workflow.NewEngine(workflows, workflowTask, notes, tickets, approvals, hub, whDispatcher, mail, log)

	aiSnapshots := repo.NewAISnapshotRepo(gdb)
	var aiSummarySvc *service.AISummaryService
	var aiDraftSvc *service.AIDraftService
	var aiTrigger service.AISummaryTrigger
	aiEnabled := aiClient != nil
	if aiEnabled {
		aiSummarySvc = service.NewAISummaryService(aiSnapshots, tickets, notes, aiClient, "")
		aiDraftSvc = service.NewAIDraftService(tickets, notes, aiClient, "", "", "")
		aiTrigger = aiSummarySvc
	}

	kbSvc := service.NewKBService(kbArticles, tickets, aiSnapshots)
	ticketSvc := service.NewTicketService(tickets, events, watchers, tags, queues, notes, queueMembers, hub, whDispatcher, engine, kbSvc, aiTrigger, log)
	noteSvc := service.NewNoteService(notes, events, watchers, tickets, hub, whDispatcher, engine, aiTrigger, log)
	problemSvc := service.NewProblemService(problems, tags)
	attachmentSvc := service.NewAttachmentService(attachments, notes, 10<<20)
	queueSvc := service.NewQueueService(queues)
	sudoSvc := service.NewSudoService(users, orgMembers, events, authMgr)
	serviceSvc := service.NewServiceCatalogService(services)
	slaBreachChecker := service.NewSLABreachChecker(tickets, events, hub, whDispatcher, log)

	srv := NewServer(
		authMgr, log, users, orgs, orgMembers, queues, queueMembers, tags, watchers,
		webhooks, workflows, workflowTask, approvals, customFields, events, tickets,
		ticketSvc, noteSvc, problemSvc, attachmentSvc, queueSvc, sudoSvc,
		serviceSvc, kbSvc,
		aiSummarySvc, aiDraftSvc, aiEnabled, engine, hub,
	)
	srv.SetDB(gdb)

	ts := httptest.NewServer(srv.Routes())
	t.Cleanup(ts.Close)

	return &testEnv{
		t: t, db: gdb, server: srv, http: ts, authMgr: authMgr,
		users: users, orgs: orgs, orgMembers: orgMembers,
		queues: queues, queueMembers: queueMembers,
		webhooks: webhooks, workflows: workflows, workflowTask: workflowTask, approvals: approvals,
		services: services, kbArticles: kbArticles, tickets: tickets,
		engine: engine, whDispatcher: whDispatcher, slaBreachChecker: slaBreachChecker,
	}
}

// client returns a fresh cookie-jar-backed HTTP client scoped to this test's
// server, so each simulated user (alice, bob, an engineer, ...) gets its own session.
func (env *testEnv) client() *client {
	jar, err := cookiejar.New(nil)
	if err != nil {
		env.t.Fatalf("cookiejar.New: %v", err)
	}
	return &client{t: env.t, base: env.http.URL, http: &http.Client{Jar: jar}}
}
