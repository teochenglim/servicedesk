package service

import (
	"servicedesk/internal/auth"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

type NoteService struct {
	notes    *repo.NoteRepo
	events   *repo.EventLogRepo
	watchers *repo.WatcherRepo

	notifier  EventPublisher
	webhooks  WebhookDispatcher
	workflows WorkflowTrigger
}

func NewNoteService(notes *repo.NoteRepo, events *repo.EventLogRepo, watchers *repo.WatcherRepo,
	notifier EventPublisher, webhooks WebhookDispatcher, workflows WorkflowTrigger) *NoteService {
	return &NoteService{notes: notes, events: events, watchers: watchers, notifier: notifier, webhooks: webhooks, workflows: workflows}
}

// Add creates a note (3.3). Internal notes are hidden from Customers by ListByTicket's includeInternal gate.
func (s *NoteService) Add(actor *auth.Claims, ticketID int64, body string, internal bool) (*models.Note, error) {
	n := &models.Note{TicketID: ticketID, AuthorID: actor.UserID, Body: body, Internal: internal}
	if err := s.notes.Create(n); err != nil {
		return nil, err
	}

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
	return n, nil
}

func (s *NoteService) ListForTicket(ticketID int64, includeInternal bool) ([]models.Note, error) {
	return s.notes.ListByTicket(ticketID, includeInternal)
}
