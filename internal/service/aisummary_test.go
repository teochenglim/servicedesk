package service

import (
	"testing"

	"servicedesk/internal/db"
	"servicedesk/internal/llm"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// newTestAISummaryService wires a real in-memory sqlite DB - Regenerate/
// EditField/RegenerateField all read/write through repo.AISnapshotRepo,
// TicketRepo, and NoteRepo, so this exercises the real merge/lock logic
// end-to-end against real rows, with only the LLM call itself faked.
func newTestAISummaryService(t *testing.T, fake *llm.FakeClient) (*AISummaryService, *repo.TicketRepo, *repo.NoteRepo) {
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
	snapshots := repo.NewAISnapshotRepo(gdb)
	return NewAISummaryService(snapshots, tickets, notes, fake, ""), tickets, notes
}

func mustCreateTicket(t *testing.T, tickets *repo.TicketRepo) int64 {
	t.Helper()
	tk := &models.Ticket{Title: "VPN drops", Description: "Client disconnects every 10 min", QueueID: 1, Status: models.StatusNew}
	if err := tickets.Create(tk); err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	return tk.ID
}

func TestAISummary_RegenerateParsesAndPersists(t *testing.T) {
	fake := &llm.FakeClient{Response: `{"symptom":"VPN drops","what_tried":"reconnect","problem_statement":"","diagnosis":"","mitigation":"","resolution":""}`}
	svc, tickets, _ := newTestAISummaryService(t, fake)
	ticketID := mustCreateTicket(t, tickets)

	if err := svc.Regenerate(ticketID, nil); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	view, err := svc.Latest(ticketID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if view.Source != "ai" {
		t.Errorf("Source = %q, want ai", view.Source)
	}
	got := fieldValue(view, "symptom")
	if got != "VPN drops" {
		t.Errorf("symptom = %q, want %q", got, "VPN drops")
	}
}

// TestAISummary_RegenerateStripsMarkdownFence covers small models that wrap
// JSON in ``` fences despite being told not to (extractJSONObject).
func TestAISummary_RegenerateStripsMarkdownFence(t *testing.T) {
	fake := &llm.FakeClient{Response: "Sure, here you go:\n```json\n{\"symptom\":\"x\",\"what_tried\":\"\",\"problem_statement\":\"\",\"diagnosis\":\"\",\"mitigation\":\"\",\"resolution\":\"\"}\n```"}
	svc, tickets, _ := newTestAISummaryService(t, fake)
	ticketID := mustCreateTicket(t, tickets)

	if err := svc.Regenerate(ticketID, nil); err != nil {
		t.Fatalf("Regenerate: %v", err)
	}
	view, _ := svc.Latest(ticketID)
	if fieldValue(view, "symptom") != "x" {
		t.Errorf("expected fenced JSON to still parse, got %+v", view.Fields)
	}
}

// TestAISummary_EditLocksFieldAgainstRegeneration is the core human-edit-
// outranks-regeneration rule (DESIGN/08 §8.9): a subsequent auto-regenerate
// must not silently overwrite a human correction.
func TestAISummary_EditLocksFieldAgainstRegeneration(t *testing.T) {
	fake := &llm.FakeClient{Response: `{"symptom":"AI guess 1","what_tried":"","problem_statement":"","diagnosis":"","mitigation":"","resolution":""}`}
	svc, tickets, _ := newTestAISummaryService(t, fake)
	ticketID := mustCreateTicket(t, tickets)

	if err := svc.Regenerate(ticketID, nil); err != nil {
		t.Fatalf("Regenerate 1: %v", err)
	}
	if err := svc.EditField(ticketID, "symptom", "human-corrected symptom"); err != nil {
		t.Fatalf("EditField: %v", err)
	}

	// Next auto-regeneration returns a different value for "symptom" - it
	// must NOT overwrite the human correction, but other fields still update.
	fake.Response = `{"symptom":"AI guess 2","what_tried":"new info","problem_statement":"","diagnosis":"","mitigation":"","resolution":""}`
	if err := svc.Regenerate(ticketID, nil); err != nil {
		t.Fatalf("Regenerate 2: %v", err)
	}

	view, _ := svc.Latest(ticketID)
	if got := fieldValue(view, "symptom"); got != "human-corrected symptom" {
		t.Errorf("symptom = %q, want the locked human correction to survive regeneration", got)
	}
	if got := fieldValue(view, "what_tried"); got != "new info" {
		t.Errorf("what_tried = %q, want the unlocked field to pick up the new AI value", got)
	}
	if !fieldLocked(view, "symptom") {
		t.Error("symptom should still be marked locked after a regeneration")
	}
}

// TestAISummary_RegenerateFieldUnlocksIt covers "regenerate this field" -
// takes a fresh AI value for just that field and unlocks it.
func TestAISummary_RegenerateFieldUnlocksIt(t *testing.T) {
	fake := &llm.FakeClient{Response: `{"symptom":"v1","what_tried":"","problem_statement":"","diagnosis":"","mitigation":"","resolution":""}`}
	svc, tickets, _ := newTestAISummaryService(t, fake)
	ticketID := mustCreateTicket(t, tickets)
	svc.Regenerate(ticketID, nil)
	svc.EditField(ticketID, "symptom", "human value")

	fake.Response = `{"symptom":"regenerated value","what_tried":"","problem_statement":"","diagnosis":"","mitigation":"","resolution":""}`
	if err := svc.RegenerateField(ticketID, "symptom"); err != nil {
		t.Fatalf("RegenerateField: %v", err)
	}

	view, _ := svc.Latest(ticketID)
	if got := fieldValue(view, "symptom"); got != "regenerated value" {
		t.Errorf("symptom = %q, want regenerated value", got)
	}
	if fieldLocked(view, "symptom") {
		t.Error("symptom should be unlocked after an explicit per-field regenerate")
	}
}

func TestAISummary_EditFieldRejectsUnknownField(t *testing.T) {
	svc, tickets, _ := newTestAISummaryService(t, &llm.FakeClient{})
	ticketID := mustCreateTicket(t, tickets)
	if err := svc.EditField(ticketID, "not_a_real_field", "x"); err == nil {
		t.Fatal("expected an error for an unknown field name")
	}
}

func TestAISummary_RegenerateSurfacesModelJSONError(t *testing.T) {
	fake := &llm.FakeClient{Response: "not json at all"}
	svc, tickets, _ := newTestAISummaryService(t, fake)
	ticketID := mustCreateTicket(t, tickets)
	if err := svc.Regenerate(ticketID, nil); err == nil {
		t.Fatal("expected an error when the model reply isn't valid JSON")
	}
}

func fieldValue(view *SummarySnapshotView, key string) string {
	for _, f := range view.Fields {
		if f.Key == key {
			return f.Value
		}
	}
	return ""
}

func fieldLocked(view *SummarySnapshotView, key string) bool {
	for _, f := range view.Fields {
		if f.Key == key {
			return f.Locked
		}
	}
	return false
}
