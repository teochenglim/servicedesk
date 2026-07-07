package service

import (
	"log/slog"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

type NoteService struct {
	notes    *repo.NoteRepo
	events   *repo.EventLogRepo
	watchers *repo.WatcherRepo
	tickets  *repo.TicketRepo

	notifier  EventPublisher
	webhooks  WebhookDispatcher
	workflows WorkflowTrigger
	aiSummary AISummaryTrigger
	log       *slog.Logger
}

func NewNoteService(notes *repo.NoteRepo, events *repo.EventLogRepo, watchers *repo.WatcherRepo, tickets *repo.TicketRepo,
	notifier EventPublisher, webhooks WebhookDispatcher, workflows WorkflowTrigger, aiSummary AISummaryTrigger, log *slog.Logger) *NoteService {
	return &NoteService{
		notes: notes, events: events, watchers: watchers, tickets: tickets,
		notifier: notifier, webhooks: webhooks, workflows: workflows, aiSummary: aiSummary, log: log,
	}
}

// Add creates a note (3.3). Internal notes are hidden from Customers by ListByTicket's includeInternal gate.
func (s *NoteService) Add(actor *auth.Claims, ticketID int64, body string, internal bool) (*models.Note, error) {
	n := &models.Note{TicketID: ticketID, AuthorID: actor.UserID, Body: body, Internal: internal}
	if err := s.notes.Create(n); err != nil {
		return nil, err
	}
	// Best-effort: keeps the Manager Activity List's recency ordering honest
	// (DESIGN/08 §8.6) - a note is "activity" even though it doesn't touch the
	// Ticket row itself. A failure here only degrades sort order, not correctness.
	_ = s.tickets.TouchUpdatedAt(ticketID)

	event := "note.added.external"
	if internal {
		event = "note.added.internal"
	}
	if s.notifier != nil {
		s.notifier.Publish(ticketID, event, n)
	}
	if s.webhooks != nil {
		s.webhooks.Dispatch(event, n)
	}
	if s.workflows != nil {
		s.workflows.Trigger("note_added", ticketID, map[string]any{"note": n})
	}
	// Re-triggered on every new note (DESIGN/08 §8.9), regenerating the whole
	// panel from full history - run in the background since an LLM call can
	// take seconds and shouldn't make posting a note feel slow. Best-effort:
	// a failure here (or the process restarting mid-call) just means the
	// panel is stale until the next note lands, not a lost/queued job - a
	// durable retry queue (like the webhook outbox) is a natural follow-up
	// if that ever needs to survive crashes.
	if s.aiSummary != nil {
		noteID := n.ID
		go func() {
			if err := s.aiSummary.Regenerate(ticketID, &noteID); err != nil {
				s.log.Warn("ai summary: regenerate failed", "ticket_id", ticketID, "note_id", noteID, "err", err)
			}
		}()
	}
	return n, nil
}

func (s *NoteService) ListForTicket(ticketID int64, includeInternal bool) ([]models.Note, error) {
	return s.notes.ListByTicket(ticketID, includeInternal)
}

// LatestForTickets returns the most recent note per ticket ID, for the
// Manager Activity List's "last message" preview (DESIGN/08 §8.6) - internal
// notes included, since Manager is staff. Notes come back oldest-first, so
// the last write per ticket ID in the loop is always the latest.
func (s *NoteService) LatestForTickets(ticketIDs []int64) (map[int64]models.Note, error) {
	notes, err := s.notes.ListForTickets(ticketIDs)
	if err != nil {
		return nil, err
	}
	latest := make(map[int64]models.Note, len(ticketIDs))
	for _, n := range notes {
		latest[n.TicketID] = n
	}
	return latest, nil
}
