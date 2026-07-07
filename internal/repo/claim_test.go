package repo

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/db"
	"servicedesk/internal/models"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := gdb.DB()
		sqlDB.Close()
	})
	return gdb
}

// TestWebhookDeliveryRepo_ClaimNext_LeasesAgainstReclaim is a regression test
// for a real bug found via live smoke testing (RELEASE/v_2.0.0.md): the
// original ClaimNext left a claimed row at status=pending with its original
// (already-due) NextRunAt, so it stayed eligible for the whole multi-second
// HTTP delivery attempt - every other worker's poll tick re-claimed and
// redelivered the same webhook (observed: the same delivery went out 4 times
// under the default WorkerPoolSize=4). ClaimNext now also pushes NextRunAt
// into the future as part of the claim, so a second ClaimNext call before
// MarkDelivered/MarkFailed must find nothing.
func TestWebhookDeliveryRepo_ClaimNext_LeasesAgainstReclaim(t *testing.T) {
	repo := NewWebhookDeliveryRepo(newTestDB(t))
	if err := repo.Enqueue(1, "ticket.created", "{}"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	d, err := repo.ClaimNext()
	if err != nil {
		t.Fatalf("first ClaimNext: %v", err)
	}
	if d.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", d.Attempts)
	}

	// Simulates another worker polling while the first delivery attempt is
	// still in flight (no MarkDelivered/MarkFailed called yet).
	if _, err := repo.ClaimNext(); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("second ClaimNext while first is in-flight: got %v, want ErrRecordNotFound", err)
	}

	// Once delivered, the row must never be reclaimed at all.
	if err := repo.MarkDelivered(d.ID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	if _, err := repo.ClaimNext(); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("ClaimNext after delivery: got %v, want ErrRecordNotFound", err)
	}
}

// TestWebhookDeliveryRepo_ClaimNext_ConcurrentCallersOnlyOneWins fires many
// concurrent ClaimNext calls at a single pending delivery (the shape of
// cmd/servicedesk/main.go's WorkerPoolSize goroutines all ticking at once)
// and asserts exactly one succeeds.
func TestWebhookDeliveryRepo_ClaimNext_ConcurrentCallersOnlyOneWins(t *testing.T) {
	repo := NewWebhookDeliveryRepo(newTestDB(t))
	if err := repo.Enqueue(1, "ticket.created", "{}"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	const workers = 8
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := repo.ClaimNext()
			results <- err
		}()
	}
	successes := 0
	for i := 0; i < workers; i++ {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("successful claims = %d, want exactly 1", successes)
	}
}

// TestWorkflowTaskRepo_ClaimNext_ConcurrentCallersOnlyOneWins is the same
// regression, for the workflow engine's task queue (identical claim shape).
func TestWorkflowTaskRepo_ClaimNext_ConcurrentCallersOnlyOneWins(t *testing.T) {
	repo := NewWorkflowTaskRepo(newTestDB(t))
	ticketID := int64(1)
	task := &models.WorkflowTask{WorkflowID: 1, TicketID: &ticketID, Status: models.TaskPending, NextRunAt: time.Now().Unix()}
	if err := repo.Create(task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	const workers = 8
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		go func() {
			_, err := repo.ClaimNext()
			results <- err
		}()
	}
	successes := 0
	for i := 0; i < workers; i++ {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Errorf("unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Errorf("successful claims = %d, want exactly 1", successes)
	}
}
