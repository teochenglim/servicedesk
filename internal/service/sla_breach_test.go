package service

import (
	"testing"
	"time"

	"servicedesk/internal/db"
	"servicedesk/internal/logging"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// fakePublisherDispatcher records fan-out calls for assertions, standing in
// for both EventPublisher and WebhookDispatcher (the real SLABreachChecker
// callers - sse.Hub and webhook.Dispatcher - both live outside this package).
type fakePublisherDispatcher struct {
	published  []string
	dispatched []string
}

func (f *fakePublisherDispatcher) Publish(ticketID int64, event string, payload any) {
	f.published = append(f.published, event)
}
func (f *fakePublisherDispatcher) Dispatch(event string, payload any) {
	f.dispatched = append(f.dispatched, event)
}

func newTestSLABreachChecker(t *testing.T) (*SLABreachChecker, *repo.TicketRepo, *fakePublisherDispatcher) {
	t.Helper()
	gdb, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := gdb.DB()
		sqlDB.Close()
	})
	tickets := repo.NewTicketRepo(gdb)
	events := repo.NewEventLogRepo(gdb)
	fake := &fakePublisherDispatcher{}
	return NewSLABreachChecker(tickets, events, fake, fake, logging.New("error")), tickets, fake
}

func TestSLABreachChecker_FiresOnceThenStops(t *testing.T) {
	checker, tickets, fake := newTestSLABreachChecker(t)
	past := time.Now().Add(-time.Hour)
	tk := &models.Ticket{Title: "Overdue", QueueID: 1, Status: models.StatusInProgress, SLADueAt: &past}
	if err := tickets.Create(tk); err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	if !checker.ProcessOne() {
		t.Fatal("expected ProcessOne to find the breaching ticket")
	}
	if len(fake.dispatched) != 1 || fake.dispatched[0] != "ticket.sla_breached" {
		t.Errorf("dispatched = %v, want [ticket.sla_breached]", fake.dispatched)
	}
	if len(fake.published) != 1 {
		t.Errorf("published = %v, want 1 entry", fake.published)
	}

	// Second call must find nothing - already claimed/notified.
	if checker.ProcessOne() {
		t.Fatal("expected no further breaching tickets after the first claim")
	}
	if len(fake.dispatched) != 1 {
		t.Errorf("expected exactly one dispatch, got %d", len(fake.dispatched))
	}
}

func TestSLABreachChecker_IgnoresClosedAndNotYetDue(t *testing.T) {
	checker, tickets, fake := newTestSLABreachChecker(t)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	tickets.Create(&models.Ticket{Title: "Closed but overdue", QueueID: 1, Status: models.StatusClosed, SLADueAt: &past})
	tickets.Create(&models.Ticket{Title: "Not due yet", QueueID: 1, Status: models.StatusInProgress, SLADueAt: &future})
	tickets.Create(&models.Ticket{Title: "No SLA set", QueueID: 1, Status: models.StatusInProgress})

	if checker.ProcessOne() {
		t.Fatal("expected no breaching tickets among closed/not-due/no-SLA tickets")
	}
	if len(fake.dispatched) != 0 {
		t.Errorf("expected no dispatches, got %v", fake.dispatched)
	}
}

func TestTicketRepo_ClaimNextBreach_RespectsReopenClearingTheFlag(t *testing.T) {
	checker, tickets, _ := newTestSLABreachChecker(t)
	past := time.Now().Add(-time.Hour)
	tk := &models.Ticket{Title: "Reopened", QueueID: 1, Status: models.StatusInProgress, SLADueAt: &past}
	tickets.Create(tk)

	if !checker.ProcessOne() {
		t.Fatal("expected the first claim to succeed")
	}
	// Simulate a reopen clearing the flag (TicketService.Transition does this).
	tk.SLABreachNotifiedAt = nil
	if err := tickets.Update(tk); err != nil {
		t.Fatalf("update: %v", err)
	}
	if !checker.ProcessOne() {
		t.Fatal("expected the ticket to be claimable again after the breach flag was cleared")
	}
}
