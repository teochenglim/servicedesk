// Package demo seeds a small, realistic dataset (orgs, queues, users,
// tickets, notes, a Problem, and a Runbook) so a fresh ServiceDesk instance
// can be demoed without a presenter clicking through empty tables. See
// RELEASE/v_1.0.8.md for the design this implements.
package demo

import (
	"fmt"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
)

// orgNames, queueSpecs, workflowName, and the "demo." username prefix are
// the fixed identifiers Seed creates and Reset/wipe use to find and remove
// exactly the demo-seeded rows, nothing else.
var orgNames = []string{"Acme Corp", "Globex Inc", "Initech"}

var queueSpecs = []struct {
	Name            string
	DefaultPriority string
	DefaultCategory string
}{
	{"Service Desk", "P3", "IT Support"},
	{"Network Ops", "P2", "Networking"},
}

// categorySpecs is the demo ticket-submission Category catalog
// (RELEASE/v_3.0.5.md) - deliberately distinct names from the guaranteed
// default Category #1 ("General", seedDefaultCategory in internal/db/db.go),
// same reasoning as queueSpecs never reusing the guaranteed default Queue #1
// ("General"): demo data is its own self-contained set, the guaranteed
// default just sits unused (but present) in demo mode.
var categorySpecs = []struct {
	Name                string
	TitleTemplate       string
	DescriptionTemplate string
}{
	{"IT Support", "", "Please describe: what were you trying to do, what happened instead, and any error message you saw."},
	{"Networking", "", "Please include: which device/location, when it started, and whether it's affecting just you or others."},
}

const (
	workflowName   = "Demo: Auto-assign & notify"
	usernamePrefix = "demo."
	demoPassword   = "demo1234"
)

// serviceSpecs is the demo Service catalog (RELEASE/v_2.1.0.md) - QueueIdx
// indexes into queueSpecs (the support queue that would pick up incidents
// against it).
var serviceSpecs = []struct {
	Name, Description string
	Criticality       models.ServiceCriticality
	QueueIdx          int
}{
	{"Mail Service", "Corporate email (hosted Exchange).", models.ServiceCriticalityCritical, 0},
	{"VPN Gateway", "Remote-access VPN concentrator.", models.ServiceCriticalityHigh, 1},
	{"Corporate Network", "Office wifi, switches, and firewall.", models.ServiceCriticalityMedium, 1},
	{"File Server", "Shared department drives.", models.ServiceCriticalityMedium, 0},
	{"Guest WiFi", "Guest wireless network.", models.ServiceCriticalityLow, 0},
}

// ticketServiceLinks maps a ticketSpecs index to a serviceSpecs index for a
// handful of demo tickets - the rest are left with no impacted service, since
// "unknown" is itself a normal, common state (RELEASE/v_2.1.0.md).
var ticketServiceLinks = map[int]int{
	0: 0, 8: 0, // email tickets -> Mail Service
	1: 1, 10: 1, 14: 1, // VPN tickets -> VPN Gateway
	4: 2, 7: 2, 12: 2, // wifi/switch/firewall tickets -> Corporate Network
	6: 3, // shared drive -> File Server
	9: 4, // guest wifi request -> Guest WiFi
}

// Empty reports whether the database has no seed data yet - used at startup
// to decide whether DemoMode (without DemoReset) should seed at all.
func Empty(db *gorm.DB) (bool, error) {
	var count int64
	if err := db.Model(&models.Organization{}).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 0, nil
}

// Seed always inserts the demo dataset; callers decide whether that's
// appropriate (see Empty, and Reset for wipe-then-reseed).
func Seed(db *gorm.DB, log *slog.Logger) error {
	return db.Transaction(func(tx *gorm.DB) error {
		orgs, err := createOrgs(tx)
		if err != nil {
			return fmt.Errorf("demo: create orgs: %w", err)
		}
		queues, err := createQueues(tx)
		if err != nil {
			return fmt.Errorf("demo: create queues: %w", err)
		}
		users, err := createUsers(tx, orgs, queues)
		if err != nil {
			return fmt.Errorf("demo: create users: %w", err)
		}
		services, err := createServices(tx, queues, users)
		if err != nil {
			return fmt.Errorf("demo: create services: %w", err)
		}
		if err := createCategories(tx); err != nil {
			return fmt.Errorf("demo: create categories: %w", err)
		}
		tickets, err := createTickets(tx, orgs, queues, users, services)
		if err != nil {
			return fmt.Errorf("demo: create tickets: %w", err)
		}
		if err := createNotes(tx, tickets, users); err != nil {
			return fmt.Errorf("demo: create notes: %w", err)
		}
		if err := createProblem(tx, tickets); err != nil {
			return fmt.Errorf("demo: create problem: %w", err)
		}
		if err := createWorkflow(tx, users); err != nil {
			return fmt.Errorf("demo: create workflow: %w", err)
		}
		if err := createKBArticles(tx, tickets, services, users); err != nil {
			return fmt.Errorf("demo: create kb articles: %w", err)
		}
		log.Info("demo: seed complete",
			"orgs", len(orgs), "queues", len(queues), "services", len(services),
			"engineers", len(users.Engineers), "customers", len(users.Customers), "tickets", len(tickets))
		return nil
	})
}

// Reset removes every row Seed is responsible for and reseeds from scratch.
// Used both for DemoReset=true (reseed on every boot) and the admin-only
// "reset demo data" endpoint.
func Reset(db *gorm.DB, log *slog.Logger) error {
	if err := wipe(db); err != nil {
		return fmt.Errorf("demo: wipe: %w", err)
	}
	return Seed(db, log)
}

func wipe(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		queueNames := make([]string, len(queueSpecs))
		for i, q := range queueSpecs {
			queueNames[i] = q.Name
		}
		var queueIDs []int64
		if err := tx.Model(&models.Queue{}).Where("name IN ?", queueNames).Pluck("id", &queueIDs).Error; err != nil {
			return err
		}

		var ticketIDs []int64
		if len(queueIDs) > 0 {
			if err := tx.Model(&models.Ticket{}).Where("queue_id IN ?", queueIDs).Pluck("id", &ticketIDs).Error; err != nil {
				return err
			}
		}

		if len(ticketIDs) > 0 {
			var problemIDs []int64
			tx.Model(&models.ProblemTicket{}).Where("ticket_id IN ?", ticketIDs).Pluck("problem_id", &problemIDs)
			var kbArticleIDs []int64
			tx.Model(&models.KBArticle{}).Where("source_ticket_id IN ?", ticketIDs).Pluck("id", &kbArticleIDs)

			for _, del := range []any{
				&models.Approval{}, &models.WorkflowTask{}, &models.Note{}, &models.Watcher{}, &models.TicketTag{},
			} {
				if err := tx.Where("ticket_id IN ?", ticketIDs).Delete(del).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("ticket_id IN ?", ticketIDs).Delete(&models.ProblemTicket{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", ticketIDs).Delete(&models.Ticket{}).Error; err != nil {
				return err
			}
			if len(problemIDs) > 0 {
				if err := tx.Where("id IN ?", problemIDs).Delete(&models.Problem{}).Error; err != nil {
					return err
				}
			}
			if len(kbArticleIDs) > 0 {
				if err := tx.Where("kb_article_id IN ?", kbArticleIDs).Delete(&models.KBArticleService{}).Error; err != nil {
					return err
				}
				if err := tx.Where("id IN ?", kbArticleIDs).Delete(&models.KBArticle{}).Error; err != nil {
					return err
				}
			}
		}

		serviceNames := make([]string, len(serviceSpecs))
		for i, s := range serviceSpecs {
			serviceNames[i] = s.Name
		}
		if err := tx.Where("name IN ?", serviceNames).Delete(&models.Service{}).Error; err != nil {
			return err
		}

		categoryNames := make([]string, len(categorySpecs))
		for i, c := range categorySpecs {
			categoryNames[i] = c.Name
		}
		if err := tx.Where("name IN ?", categoryNames).Delete(&models.Category{}).Error; err != nil {
			return err
		}

		if err := tx.Where("name = ?", workflowName).Delete(&models.Workflow{}).Error; err != nil {
			return err
		}

		var userIDs []int64
		if err := tx.Model(&models.User{}).Where("username LIKE ?", usernamePrefix+"%").Pluck("id", &userIDs).Error; err != nil {
			return err
		}
		if len(userIDs) > 0 {
			if err := tx.Where("user_id IN ?", userIDs).Delete(&models.OrgMembership{}).Error; err != nil {
				return err
			}
			if err := tx.Where("user_id IN ?", userIDs).Delete(&models.QueueMembership{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", userIDs).Delete(&models.User{}).Error; err != nil {
				return err
			}
		}
		if len(queueIDs) > 0 {
			if err := tx.Where("id IN ?", queueIDs).Delete(&models.Queue{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("name IN ?", orgNames).Delete(&models.Organization{}).Error; err != nil {
			return err
		}
		return nil
	})
}

func createOrgs(tx *gorm.DB) ([]models.Organization, error) {
	orgs := make([]models.Organization, len(orgNames))
	for i, name := range orgNames {
		orgs[i] = models.Organization{Name: name}
		if err := tx.Create(&orgs[i]).Error; err != nil {
			return nil, err
		}
	}
	return orgs, nil
}

func createQueues(tx *gorm.DB) ([]models.Queue, error) {
	queues := make([]models.Queue, len(queueSpecs))
	for i, spec := range queueSpecs {
		queues[i] = models.Queue{Name: spec.Name, DefaultPriority: spec.DefaultPriority, DefaultCategory: spec.DefaultCategory}
		if err := tx.Create(&queues[i]).Error; err != nil {
			return nil, err
		}
	}
	return queues, nil
}

type seededUsers struct {
	Manager   models.User
	Engineers []models.User
	Customers []models.User
}

// createUsers makes 1 Manager, 4 Engineers (split 2/2 across the 2 demo
// queues), and 6 Customers (split 2/2/2 across the 3 demo orgs).
func createUsers(tx *gorm.DB, orgs []models.Organization, queues []models.Queue) (seededUsers, error) {
	hash, err := auth.HashPassword(demoPassword)
	if err != nil {
		return seededUsers{}, err
	}

	qadmin := models.User{
		Username: usernamePrefix + "admin", Email: "demo.admin@example.com",
		PasswordHash: hash, Role: models.RoleManager, Source: "demo",
	}
	if err := tx.Create(&qadmin).Error; err != nil {
		return seededUsers{}, err
	}

	engineers := make([]models.User, 4)
	for i := range engineers {
		engineers[i] = models.User{
			Username:     fmt.Sprintf("%sengineer%d", usernamePrefix, i+1),
			Email:        fmt.Sprintf("demo.engineer%d@example.com", i+1),
			PasswordHash: hash, Role: models.RoleEngineer, Source: "demo",
		}
		if err := tx.Create(&engineers[i]).Error; err != nil {
			return seededUsers{}, err
		}
		queue := queues[i/2]
		if err := tx.Create(&models.QueueMembership{QueueID: queue.ID, UserID: engineers[i].ID}).Error; err != nil {
			return seededUsers{}, err
		}
	}

	customers := make([]models.User, 6)
	for i := range customers {
		customers[i] = models.User{
			Username:     fmt.Sprintf("%scustomer%d", usernamePrefix, i+1),
			Email:        fmt.Sprintf("demo.customer%d@example.com", i+1),
			PasswordHash: hash, Role: models.RoleCustomer, Source: "demo",
		}
		if err := tx.Create(&customers[i]).Error; err != nil {
			return seededUsers{}, err
		}
		org := orgs[i/2]
		if err := tx.Create(&models.OrgMembership{OrgID: org.ID, UserID: customers[i].ID}).Error; err != nil {
			return seededUsers{}, err
		}
	}

	return seededUsers{Manager: qadmin, Engineers: engineers, Customers: customers}, nil
}

// createServices seeds the demo Service catalog (RELEASE/v_2.1.0.md), all
// owned by the demo Manager for simplicity.
func createServices(tx *gorm.DB, queues []models.Queue, users seededUsers) ([]models.Service, error) {
	services := make([]models.Service, len(serviceSpecs))
	for i, spec := range serviceSpecs {
		queueID := queues[spec.QueueIdx].ID
		services[i] = models.Service{
			Name: spec.Name, Description: spec.Description, Criticality: spec.Criticality,
			Status: models.ServiceStatusActive, OwnerID: &users.Manager.ID, SupportQueueID: &queueID,
		}
		if err := tx.Create(&services[i]).Error; err != nil {
			return nil, err
		}
	}
	return services, nil
}

// createCategories seeds the demo ticket-submission Category catalog
// (RELEASE/v_3.0.5.md) - all top-level (no ParentID), matching this cycle's
// flat-dropdown scope.
func createCategories(tx *gorm.DB) error {
	for _, spec := range categorySpecs {
		c := models.Category{Name: spec.Name, TitleTemplate: spec.TitleTemplate, DescriptionTemplate: spec.DescriptionTemplate}
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
	}
	return nil
}

// ticketSpec describes one seeded ticket. CustomerIdx/EngineerIdx/QueueIdx
// index into the slices createUsers/createQueues returned; the customer's
// org (via the 2-per-org split in createUsers) becomes the ticket's OrgID.
type ticketSpec struct {
	Title, Description, Category string
	Priority                     models.Priority
	Status                       models.TicketStatus
	QueueIdx, CustomerIdx        int
	EngineerIdx                  *int // nil = unassigned
	SLA                          string
}

func eng(i int) *int { return &i }

var ticketSpecs = []ticketSpec{
	{"Cannot access email", "Login fails with 'account locked' after 3 attempts.", "IT Support", models.PriorityP2, models.StatusNew, 0, 0, nil, "ok"},
	{"VPN drops every 10 minutes", "Client disconnects repeatedly, has to reauth each time.", "Networking", models.PriorityP1, models.StatusInProgress, 1, 1, eng(2), "warning"},
	{"Printer offline on 3rd floor", "HP LaserJet shows offline in Windows.", "IT Support", models.PriorityP4, models.StatusResolved, 0, 2, eng(0), "ok"},
	{"Password reset request", "Forgot password, needs a manual reset.", "IT Support", models.PriorityP3, models.StatusClosed, 0, 3, eng(1), "ok"},
	{"Office wifi outage", "Entire floor lost wifi around 9am.", "Networking", models.PriorityP1, models.StatusInProgress, 1, 4, eng(3), "breach"},
	{"New laptop provisioning", "New hire starts Monday, needs a laptop imaged.", "IT Support", models.PriorityP3, models.StatusNew, 0, 5, nil, "ok"},
	{"Shared drive permissions", "Can't access the Finance shared drive anymore.", "IT Support", models.PriorityP2, models.StatusInProgress, 0, 0, eng(1), "warning"},
	{"Network switch flapping", "Switch in IDF-2 keeps dropping ports.", "Networking", models.PriorityP1, models.StatusNew, 1, 1, eng(2), "breach"},
	{"Email spam filter too aggressive", "Legitimate vendor emails going to junk.", "IT Support", models.PriorityP3, models.StatusResolved, 0, 2, eng(0), "ok"},
	{"Guest wifi access request", "Need a temporary guest wifi code for visitors.", "Networking", models.PriorityP4, models.StatusClosed, 1, 3, eng(3), "ok"},
	{"Slow VPN performance", "VPN throughput under 2mbps during business hours.", "Networking", models.PriorityP2, models.StatusInProgress, 1, 4, eng(2), "warning"},
	{"Monitor flickering", "External monitor flickers intermittently.", "IT Support", models.PriorityP4, models.StatusNew, 0, 5, nil, "ok"},
	{"Firewall rule request", "Need an inbound rule opened for a vendor API.", "Networking", models.PriorityP2, models.StatusNew, 1, 0, eng(3), "ok"},
	{"Software license renewal", "Design software license expired, needs renewal.", "IT Support", models.PriorityP3, models.StatusResolved, 0, 1, eng(1), "ok"},
	{"Site-to-site VPN down", "Branch office tunnel has been down since last night.", "Networking", models.PriorityP1, models.StatusInProgress, 1, 2, eng(2), "breach"},
}

func createTickets(tx *gorm.DB, orgs []models.Organization, queues []models.Queue, users seededUsers, services []models.Service) ([]models.Ticket, error) {
	tickets := make([]models.Ticket, len(ticketSpecs))
	for i, sp := range ticketSpecs {
		var assignee *int64
		if sp.EngineerIdx != nil {
			id := users.Engineers[*sp.EngineerIdx].ID
			assignee = &id
		}
		var serviceID *int64
		if svcIdx, ok := ticketServiceLinks[i]; ok {
			id := services[svcIdx].ID
			serviceID = &id
		}
		customer := users.Customers[sp.CustomerIdx]
		org := orgs[sp.CustomerIdx/2]
		detectedAt, ackedAt, mitigatedAt, resolvedAt := demoStageTimestamps(sp.Status, time.Now())
		tickets[i] = models.Ticket{
			Title: sp.Title, Description: sp.Description, Priority: sp.Priority, Status: sp.Status,
			QueueID: queues[sp.QueueIdx].ID, Category: sp.Category, AssigneeID: assignee,
			CreatorID: customer.ID, OrgID: org.ID, ServiceID: serviceID, SLADueAt: slaDue(sp.SLA),
			DetectedAt: detectedAt, AckedAt: ackedAt, MitigatedAt: mitigatedAt, ResolvedAt: resolvedAt,
		}
		if err := tx.Create(&tickets[i]).Error; err != nil {
			return nil, err
		}
	}
	return tickets, nil
}

// demoStageTimestamps fabricates plausible stage-tracking timestamps
// (DESIGN/03 §3.1.2b) matching a seeded ticket's Status - demo tickets are
// inserted directly with a final Status rather than replayed through
// TicketService, so there's no event_logs history to derive these from
// (see db.backfillStageTimestamps, which only has real history to work with
// for genuinely-created tickets). Without this, every demo ticket would show
// the progress bar stuck at "Detect" regardless of its actual status.
func demoStageTimestamps(status models.TicketStatus, now time.Time) (detected, acked, mitigated, resolved *time.Time) {
	d := now.Add(-24 * time.Hour)
	detected = &d
	switch status {
	case models.StatusInProgress:
		a := now.Add(-3 * time.Hour)
		acked = &a
	case models.StatusResolved, models.StatusClosed:
		a := now.Add(-20 * time.Hour)
		m := now.Add(-8 * time.Hour)
		r := now.Add(-2 * time.Hour)
		acked, mitigated, resolved = &a, &m, &r
	}
	return detected, acked, mitigated, resolved
}

func slaDue(bucket string) *time.Time {
	now := time.Now()
	var t time.Time
	switch bucket {
	case "breach":
		t = now.Add(-3 * time.Hour)
	case "warning":
		t = now.Add(1 * time.Hour)
	default:
		t = now.Add(48 * time.Hour)
	}
	return &t
}

// createNotes gives the 3 VPN/network tickets (indices 1, 4, 10 in
// ticketSpecs) a conversation thread mixing customer, agent, and
// internal-only notes, to populate the three-way thread styling from v1.0.7.
func createNotes(tx *gorm.DB, tickets []models.Ticket, users seededUsers) error {
	threads := []struct {
		TicketIdx int
		Notes     []models.Note
	}{
		{1, []models.Note{
			{AuthorID: users.Customers[1].ID, Body: "This has been happening all week, very disruptive.", Internal: false},
			{AuthorID: users.Engineers[2].ID, Body: "Checking the VPN concentrator logs now.", Internal: false},
			{AuthorID: users.Engineers[2].ID, Body: "Concentrator is dropping idle sessions after 10 min - likely a timeout misconfig.", Internal: true},
		}},
		{4, []models.Note{
			{AuthorID: users.Customers[4].ID, Body: "Whole floor is affected, can't get any work done.", Internal: false},
			{AuthorID: users.Engineers[3].ID, Body: "AP controller rebooting now, should be back in 5 minutes.", Internal: false},
		}},
		{10, []models.Note{
			{AuthorID: users.Customers[4].ID, Body: "Uploads to the file server are timing out.", Internal: false},
			{AuthorID: users.Engineers[2].ID, Body: "Same root cause as the earlier outage - tracking under the linked Problem.", Internal: true},
		}},
	}
	for _, thread := range threads {
		ticketID := tickets[thread.TicketIdx].ID
		for _, n := range thread.Notes {
			n.TicketID = ticketID
			if err := tx.Create(&n).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// createProblem links the 3 network-outage tickets under one Problem record
// so Problem Management isn't empty in the demo either.
func createProblem(tx *gorm.DB, tickets []models.Ticket) error {
	p := models.Problem{
		Title:              "Recurring VPN/network instability",
		RootCause:          "VPN concentrator idle-session timeout set too aggressively, compounded by an undersized AP controller at the branch office.",
		Resolution:         "Timeout raised to 60 minutes; AP controller firmware scheduled for upgrade.",
		PreventiveMeasures: "Add synthetic VPN-session monitoring to catch timeout regressions before customers report them.",
	}
	if err := tx.Create(&p).Error; err != nil {
		return err
	}
	for _, idx := range []int{1, 4, 10} {
		if err := tx.Create(&models.ProblemTicket{ProblemID: p.ID, TicketID: tickets[idx].ID}).Error; err != nil {
			return err
		}
	}
	return nil
}

// createKBArticles seeds two Knowledge Base Feedback Loop articles
// (DESIGN/08 §8.10, RELEASE/v_2.1.0.md): one already published (so /kb isn't
// empty in the demo) and one still a draft (so /kb/review has something to
// curate). Both are sourced from already-Resolved demo tickets, mirroring
// what KBService.ProposeFromTicket would produce.
func createKBArticles(tx *gorm.DB, tickets []models.Ticket, services []models.Service, users seededUsers) error {
	// ticketSpecs[8] "Email spam filter too aggressive" (Resolved) -> Mail Service.
	publishedTicketID := tickets[8].ID
	now := time.Now()
	published := models.KBArticle{
		Title:            "Vendor emails going to spam",
		Status:           models.KBStatusPublished,
		Symptom:          "Legitimate vendor emails are landing in the junk/spam folder instead of the inbox.",
		WhatToObserve:    "Expected emails from known vendors are missing from the inbox but present in Junk Email.",
		SelfServiceSteps: "Check the Junk Email folder and mark the sender as \"Not Junk\"; add the sender to your Safe Senders list.",
		Resolution:       "The spam filter's sender-reputation threshold was tuned down for known vendor domains; affected senders were added to the allow list.",
		Environment:      "Corporate email (hosted Exchange), all users.",
		RootCause:        "Spam filter's sender-reputation threshold was too aggressive for newer vendor sending domains.",
		ValidationSteps:  "Reproduced by sending a test message from the affected vendor domain and confirming it landed in Junk; confirmed clean after allow-listing.",
		ResolutionSteps:  "Added the vendor domains to the mail gateway's allow list and lowered the reputation-score threshold by one tier.",
		Workaround:       "Mark individual messages as \"Not Junk\" until the allow-list change propagates.",
		BlastRadius:      "All Mail Service users; no data loss, messages were quarantined not deleted.",
		SourceTicketID:   &publishedTicketID,
		CreatedByID:      users.Engineers[0].ID,
		ApprovedByID:     &users.Manager.ID,
		PublishedAt:      &now,
	}
	if err := tx.Create(&published).Error; err != nil {
		return err
	}
	if err := tx.Create(&models.KBArticleService{KBArticleID: published.ID, ServiceID: services[0].ID}).Error; err != nil {
		return err
	}

	// ticketSpecs[2] "Printer offline on 3rd floor" (Resolved) - no Service
	// link (printer isn't in the demo catalog), left as a draft awaiting
	// curation - illustrates the propose-on-resolve -> review queue step
	// before anything reaches the Customer-facing /kb.
	draftTicketID := tickets[2].ID
	draft := models.KBArticle{
		Title:          "Printer shows offline in Windows",
		Status:         models.KBStatusDraft,
		Symptom:        "Printer shows as \"offline\" in Windows even though it's powered on and connected.",
		Resolution:     "Removed and re-added the printer using its static IP address.",
		RootCause:      "Printer's DHCP lease changed, but the Windows printer port still pointed at the old IP.",
		SourceTicketID: &draftTicketID,
		CreatedByID:    users.Engineers[0].ID,
	}
	return tx.Create(&draft).Error
}

// createWorkflow adds one Runbook (auto_assign + notify) so automation isn't
// a dead feature in the demo - it shows up in /admin/workflows and as a
// manually-startable "Runbook" on any ticket in the Network Ops queue.
func createWorkflow(tx *gorm.DB, users seededUsers) error {
	config := fmt.Sprintf(
		`{"steps":[{"id":"assign","type":"auto_assign","assignee_id":%d},{"id":"note","type":"add_note","body":"Auto-assigned by the demo runbook.","internal":true},{"id":"notify","type":"notify","message":"Ticket auto-assigned during demo runbook run."}]}`,
		users.Engineers[0].ID,
	)
	wf := models.Workflow{
		Name: workflowName, Trigger: "ticket_created", IsRunbook: true, Config: config, Active: true,
	}
	return tx.Create(&wf).Error
}
