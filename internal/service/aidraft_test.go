package service

import (
	"strings"
	"testing"

	"servicedesk/internal/db"
	"servicedesk/internal/llm"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

func newTestAIDraftService(t *testing.T, fake *llm.FakeClient) (*AIDraftService, *repo.TicketRepo, *repo.NoteRepo) {
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
	notes := repo.NewNoteRepo(gdb)
	return NewAIDraftService(tickets, notes, fake, "", "", ""), tickets, notes
}

// TestAIDraft_DescriptionNeedsNoTicket covers drafting on ticket submission,
// before any Ticket row exists - a stateless one-shot call, no persistence.
func TestAIDraft_DescriptionNeedsNoTicket(t *testing.T) {
	fake := &llm.FakeClient{Response: "Checkout fails intermittently with a 504, started this morning."}
	svc, _, _ := newTestAIDraftService(t, fake)

	got, err := svc.DraftDescription("checkout broken sometimes")
	if err != nil {
		t.Fatalf("DraftDescription: %v", err)
	}
	if got != fake.Response {
		t.Errorf("got %q, want %q", got, fake.Response)
	}
	if len(fake.Calls) != 1 || fake.Calls[0][1].Content != "checkout broken sometimes" {
		t.Errorf("expected the rough text forwarded as the user message, got %+v", fake.Calls)
	}
}

func TestAIDraft_ResolutionAndTransferPullTicketHistory(t *testing.T) {
	fake := &llm.FakeClient{Response: "Root cause was an expired cert; renewed manually."}
	svc, tickets, notes := newTestAIDraftService(t, fake)
	ticketID := mustCreateTicket(t, tickets)
	if err := notes.Create(&models.Note{TicketID: ticketID, Body: "Confirmed cert expired on edge-gw-3."}); err != nil {
		t.Fatalf("create note: %v", err)
	}

	got, err := svc.DraftResolutionNote(ticketID)
	if err != nil {
		t.Fatalf("DraftResolutionNote: %v", err)
	}
	if got != fake.Response {
		t.Errorf("got %q, want %q", got, fake.Response)
	}
	lastCall := fake.Calls[len(fake.Calls)-1]
	if !strings.Contains(lastCall[1].Content, "Confirmed cert expired") {
		t.Errorf("expected note history in the prompt, got %q", lastCall[1].Content)
	}

	if _, err := svc.DraftTransferReason(ticketID); err != nil {
		t.Fatalf("DraftTransferReason: %v", err)
	}
}
