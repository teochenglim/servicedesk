package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"servicedesk/internal/auth"
	"servicedesk/internal/config"
	"servicedesk/internal/db"
	"servicedesk/internal/demo"
	"servicedesk/internal/httpapi"
	"servicedesk/internal/llm"
	"servicedesk/internal/logging"
	"servicedesk/internal/mailer"
	"servicedesk/internal/repo"
	"servicedesk/internal/service"
	"servicedesk/internal/sse"
	"servicedesk/internal/webhook"
	"servicedesk/internal/workflow"
)

// version is set at build time via -ldflags "-X main.version=...", see
// .github/workflows/release.yml. Defaults to "dev" for local builds.
var version = "dev"

func main() {
	cfg := config.Load()
	log := logging.New(cfg.LogLevel)
	log.Info("startup: servicedesk", "version", version)

	gdb, err := db.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		logging.Fatal(log, "startup: failed to open database", "driver", cfg.DBDriver, "err", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		logging.Fatal(log, "startup: failed to get underlying sql.DB", "err", err)
	}
	defer sqlDB.Close()

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
	workflowTasks := repo.NewWorkflowTaskRepo(gdb)
	approvals := repo.NewApprovalRepo(gdb)
	customFields := repo.NewCustomFieldRepo(gdb)
	events := repo.NewEventLogRepo(gdb)
	attachments := repo.NewAttachmentRepo(gdb)
	aiSnapshots := repo.NewAISnapshotRepo(gdb)
	services := repo.NewServiceRepo(gdb)
	categories := repo.NewCategoryRepo(gdb)
	kbArticles := repo.NewKBArticleRepo(gdb)

	if err := auth.Bootstrap(users, cfg, log); err != nil {
		logging.Fatal(log, "startup: failed to bootstrap users", "err", err)
	}

	// Demo seeding runs after auth.Bootstrap, never before: Bootstrap relies on
	// the "system" actor being the very first row in the users table (see
	// auth.SystemActorID) to land on ID 1.
	if cfg.SeedDemoOnly {
		if err := demo.Seed(gdb, log); err != nil {
			logging.Fatal(log, "demo: seed failed", "err", err)
		}
		log.Info("demo: seed complete, exiting (SEED_DEMO_ONLY)")
		return
	}
	if cfg.DemoMode {
		if cfg.DemoReset {
			if err := demo.Reset(gdb, log); err != nil {
				logging.Fatal(log, "demo: reset failed", "err", err)
			}
		} else if empty, err := demo.Empty(gdb); err != nil {
			logging.Fatal(log, "demo: could not check for existing data", "err", err)
		} else if empty {
			if err := demo.Seed(gdb, log); err != nil {
				logging.Fatal(log, "demo: seed failed", "err", err)
			}
		} else {
			log.Info("demo: DEMO_MODE set but database already has data, skipping seed")
		}
	}

	authMgr := auth.NewManager(cfg.JWTSecret, cfg.JWTIssuer)

	hub := sse.NewHub(watchers, tickets)
	whDispatcher := webhook.NewDispatcher(webhooks, webhookDeliveries, log)
	mail := mailer.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPFrom, cfg.SMTPUser, cfg.SMTPPass, log)
	engine := workflow.NewEngine(workflows, workflowTasks, notes, tickets, approvals, hub, whDispatcher, mail, log)

	// AI-assisted drafting + AI Ticket Intelligence Panel (DESIGN/08 §8.8-8.9) -
	// off by default (cfg.AIEnabled). aiTrigger is left as a true nil interface
	// (not a typed-nil *AISummaryService) when disabled, so NoteService's plain
	// "if s.aiSummary != nil" check works correctly.
	var aiSummarySvc *service.AISummaryService
	var aiDraftSvc *service.AIDraftService
	var aiTrigger service.AISummaryTrigger
	if cfg.AIEnabled {
		llmClient := llm.NewHTTPClient(cfg.LLMBaseURL, cfg.LLMAPIKey, cfg.LLMModel)
		aiSummarySvc = service.NewAISummaryService(aiSnapshots, tickets, notes, llmClient, cfg.AISummaryPrompt)
		aiDraftSvc = service.NewAIDraftService(tickets, notes, llmClient, cfg.AIDraftDescriptionPrompt, cfg.AIDraftResolutionPrompt, cfg.AIDraftTransferPrompt)
		aiTrigger = aiSummarySvc
	}

	// Knowledge Base Feedback Loop (DESIGN/08 §8.10, RELEASE/v_2.1.0.md) - not
	// gated behind cfg.AIEnabled: ProposeFromTicket falls back to empty seed
	// fields when there's no AI snapshot, so curators can draft/publish
	// articles manually even with AI features off.
	kbSvc := service.NewKBService(kbArticles, tickets, aiSnapshots)

	ticketSvc := service.NewTicketService(tickets, events, watchers, tags, queues, notes, queueMembers, users, orgMembers, hub, whDispatcher, engine, kbSvc, aiTrigger, log)
	noteSvc := service.NewNoteService(notes, events, watchers, tickets, hub, whDispatcher, engine, aiTrigger, log)
	problemSvc := service.NewProblemService(problems, tags)
	attachmentSvc := service.NewAttachmentService(attachments, notes, int64(cfg.AttachmentMaxSizeBytes))
	queueSvc := service.NewQueueService(queues)
	sudoSvc := service.NewSudoService(users, orgMembers, events, authMgr)
	serviceSvc := service.NewServiceCatalogService(services)
	categorySvc := service.NewCategoryService(categories)
	slaBreachChecker := service.NewSLABreachChecker(tickets, events, hub, whDispatcher, log)

	server := httpapi.NewServer(
		authMgr, log, users, orgs, orgMembers, queues, queueMembers, tags, watchers, webhooks, workflows, workflowTasks,
		approvals, customFields, events, tickets, ticketSvc, noteSvc, problemSvc, attachmentSvc, queueSvc, sudoSvc,
		serviceSvc, categorySvc, kbSvc,
		aiSummarySvc, aiDraftSvc, cfg.AIEnabled, engine, hub,
	)
	server.SetDB(gdb)
	server.SetDemoMode(cfg.DemoMode)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Background worker pool (DESIGN.md 3.5/6.1): sqlite serializes writes to a
	// single connection, so extra goroutines add claim concurrency, not raw
	// write throughput, but keep the architecture ready to swap in Postgres/MySQL.
	pollInterval := time.Duration(cfg.WorkerPollMillis) * time.Millisecond
	for i := 0; i < cfg.WorkerPoolSize; i++ {
		go whDispatcher.Run(ctx, pollInterval)
		go engine.Run(ctx, pollInterval)
		go slaBreachChecker.Run(ctx, pollInterval)
	}
	log.Info("startup: background workers running", "pool_size", cfg.WorkerPoolSize, "poll_interval", pollInterval)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("startup: listening", "addr", cfg.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logging.Fatal(log, "server: listen failed", "err", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutdown: signal received, draining connections")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown: graceful shutdown failed", "err", err)
	}
	log.Info("shutdown: complete")
}
