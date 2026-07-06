package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

var ErrForbidden = errors.New("forbidden")

type TicketService struct {
	tickets      *repo.TicketRepo
	events       *repo.EventLogRepo
	watchers     *repo.WatcherRepo
	tags         *repo.TagRepo
	queues       *repo.QueueRepo
	notes        *repo.NoteRepo
	queueMembers *repo.QueueMembershipRepo

	notifier  EventPublisher
	webhooks  WebhookDispatcher
	workflows WorkflowTrigger
	log       *slog.Logger
}

func NewTicketService(
	tickets *repo.TicketRepo, events *repo.EventLogRepo, watchers *repo.WatcherRepo,
	tags *repo.TagRepo, queues *repo.QueueRepo, notes *repo.NoteRepo, queueMembers *repo.QueueMembershipRepo,
	notifier EventPublisher, webhooks WebhookDispatcher, workflows WorkflowTrigger, log *slog.Logger,
) *TicketService {
	return &TicketService{
		tickets: tickets, events: events, watchers: watchers, tags: tags, queues: queues, notes: notes,
		queueMembers: queueMembers,
		notifier:     notifier, webhooks: webhooks, workflows: workflows, log: log,
	}
}

type CreateTicketInput struct {
	Title        string
	Description  string
	Priority     models.Priority
	QueueID      int64
	Category     string
	CustomFields map[string]any
}

func (s *TicketService) Create(actor *auth.Claims, in CreateTicketInput) (*models.Ticket, error) {
	q, err := s.queues.Get(in.QueueID)
	if err != nil {
		return nil, fmt.Errorf("queue not found: %w", err)
	}
	priority := in.Priority
	if priority == "" {
		priority = models.Priority(q.DefaultPriority)
	}
	category := in.Category
	if category == "" {
		category = q.DefaultCategory
	}
	cf, _ := json.Marshal(in.CustomFields)

	t := &models.Ticket{
		Title:        in.Title,
		Description:  in.Description,
		Priority:     priority,
		Status:       models.StatusNew,
		QueueID:      in.QueueID,
		Category:     category,
		CreatorID:    actor.UserID,
		OrgID:        actor.OrgID,
		CustomFields: string(cf),
	}
	if err := s.tickets.Create(t); err != nil {
		return nil, err
	}

	if err := s.watchers.Add(t.ID, actor.UserID); err != nil {
		s.log.Error("ticket create: auto-watch by creator failed", "ticket_id", t.ID, "user_id", actor.UserID, "err", err)
	}
	s.logEvent(t.ID, &actor.UserID, "ticket_created", map[string]any{"title": t.Title})
	s.fanOut("ticket.created", t.ID, t)
	s.workflows.Trigger("ticket_created", t.ID, map[string]any{"ticket": t})
	return t, nil
}

func (s *TicketService) Get(id int64) (*models.Ticket, error) {
	return s.tickets.Get(id)
}

func (s *TicketService) List(f repo.ListFilter) ([]models.Ticket, error) {
	return s.tickets.List(f)
}

// Transition applies the explicit ITSM state machine (DESIGN.md 3.1.2),
// enforcing that only an Admin or the original creator may reopen a Closed ticket.
func (s *TicketService) Transition(actor *auth.Claims, ticketID int64, action Action, reason string) (*models.Ticket, error) {
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return nil, err
	}

	if t.Status == models.StatusClosed && action == ActionReopen {
		if actor.Role != models.RoleSystemAdmin && actor.UserID != t.CreatorID {
			return nil, ErrForbidden
		}
	}

	final, path, err := nextStatus(t.Status, action)
	if err != nil {
		return nil, err
	}

	from := t.Status
	t.Status = final
	if err := s.tickets.Update(t); err != nil {
		return nil, err
	}

	cursor := from
	for _, st := range path {
		s.logEvent(t.ID, &actor.UserID, "status_changed", map[string]any{
			"from": cursor, "to": st, "action": action, "reason": reason,
		})
		cursor = st
	}

	s.fanOut("ticket.status_changed", t.ID, map[string]any{"ticket": t, "from": from, "to": final})
	s.workflows.Trigger("status_changed", t.ID, map[string]any{"ticket": t, "from": from, "to": final})
	return t, nil
}

// Pickup lets an agent self-assign a ticket out of a queue.
func (s *TicketService) Pickup(actor *auth.Claims, ticketID int64) (*models.Ticket, error) {
	return s.assign(actor, ticketID, actor.UserID, ActionPickup)
}

// Assign lets an Admin/manager assign a ticket to a specific agent.
func (s *TicketService) Assign(actor *auth.Claims, ticketID, assigneeID int64) (*models.Ticket, error) {
	return s.assign(actor, ticketID, assigneeID, ActionAssign)
}

func (s *TicketService) assign(actor *auth.Claims, ticketID, assigneeID int64, action Action) (*models.Ticket, error) {
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return nil, err
	}

	// Engineers may only pick up/be assigned tickets in a queue they belong to
	// (e.g. an "Engineers" or "Networking" queue); QueueAdmin/SystemAdmin bypass this.
	if actor.Role == models.RoleEngineer {
		member, err := s.queueMembers.IsMember(t.QueueID, assigneeID)
		if err != nil {
			return nil, err
		}
		if !member {
			return nil, fmt.Errorf("%w: not a member of this ticket's queue", ErrForbidden)
		}
	}

	t.AssigneeID = &assigneeID
	if t.Status == models.StatusNew {
		final, _, err := nextStatus(t.Status, action)
		if err == nil {
			t.Status = final
		}
	}
	if err := s.tickets.Update(t); err != nil {
		return nil, err
	}
	s.logEvent(t.ID, &actor.UserID, "assigned", map[string]any{"assignee_id": assigneeID, "action": action})
	s.fanOut("ticket.assigned", t.ID, t)
	s.workflows.Trigger("field_updated", t.ID, map[string]any{"ticket": t, "field": "assignee_id"})
	return t, nil
}

type UpdateFieldsInput struct {
	Title        *string
	Description  *string
	Priority     *models.Priority
	Category     *string
	CustomFields map[string]any
}

func (s *TicketService) UpdateFields(actor *auth.Claims, ticketID int64, in UpdateFieldsInput) (*models.Ticket, error) {
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return nil, err
	}
	changed := map[string]any{}
	if in.Title != nil {
		t.Title, changed["title"] = *in.Title, *in.Title
	}
	if in.Description != nil {
		t.Description, changed["description"] = *in.Description, *in.Description
	}
	if in.Priority != nil {
		t.Priority, changed["priority"] = *in.Priority, *in.Priority
	}
	if in.Category != nil {
		t.Category, changed["category"] = *in.Category, *in.Category
	}
	if in.CustomFields != nil {
		cf, _ := json.Marshal(in.CustomFields)
		t.CustomFields = string(cf)
		changed["custom_fields"] = in.CustomFields
	}
	if err := s.tickets.Update(t); err != nil {
		return nil, err
	}
	s.logEvent(t.ID, &actor.UserID, "field_updated", changed)
	s.fanOut("ticket.updated", t.ID, t)
	s.workflows.Trigger("field_updated", t.ID, map[string]any{"ticket": t, "changed": changed})
	return t, nil
}

func (s *TicketService) Watch(userID, ticketID int64) error { return s.watchers.Add(ticketID, userID) }
func (s *TicketService) Unwatch(userID, ticketID int64) error {
	return s.watchers.Remove(ticketID, userID)
}

func (s *TicketService) AddLabel(actor *auth.Claims, ticketID int64, name, kind string) error {
	tag, err := s.tags.GetOrCreate(name, kind)
	if err != nil {
		return err
	}
	if err := s.tags.AttachToTicket(ticketID, tag.ID); err != nil {
		return err
	}
	s.logEvent(ticketID, &actor.UserID, "label_added", map[string]any{"name": name, "kind": kind})
	s.fanOut("ticket.label_added", ticketID, map[string]any{"name": name, "kind": kind})
	return nil
}

func (s *TicketService) RemoveLabel(actor *auth.Claims, ticketID, tagID int64) error {
	if err := s.tags.DetachFromTicket(ticketID, tagID); err != nil {
		return err
	}
	s.logEvent(ticketID, &actor.UserID, "label_removed", map[string]any{"tag_id": tagID})
	return nil
}

func (s *TicketService) logEvent(ticketID int64, actorID *int64, event string, details map[string]any) {
	b, _ := json.Marshal(details)
	if err := s.events.Append(&models.EventLog{TicketID: &ticketID, ActorID: actorID, Event: event, Details: string(b)}); err != nil {
		s.log.Error("audit log write failed", "ticket_id", ticketID, "event", event, "err", err)
	}
}

func (s *TicketService) fanOut(event string, ticketID int64, payload any) {
	if s.notifier != nil {
		s.notifier.Publish(ticketID, event, payload)
	}
	if s.webhooks != nil {
		s.webhooks.Dispatch(event, payload)
	}
}
