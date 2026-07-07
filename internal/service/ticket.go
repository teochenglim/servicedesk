package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"servicedesk/internal/auth"
	"servicedesk/internal/metrics"
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
	// DetectedAt backdates the stage-tracking overlay's Detect stage to an
	// earlier monitoring trigger time (DESIGN/03 §3.1.2b) - only honored when
	// actor holds CapAgentDetect; ignored otherwise so an ordinary Customer or
	// Engineer can't corrupt the MTTD metric by backdating their own ticket.
	DetectedAt *time.Time
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

	now := time.Now()
	detectedAt := now
	if in.DetectedAt != nil && actor.Role.Can(models.CapAgentDetect) {
		detectedAt = *in.DetectedAt
	}

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
		DetectedAt:   &detectedAt,
		SLADueAt:     slaDueAt(q, priority, category, now),
	}
	if err := s.tickets.Create(t); err != nil {
		return nil, err
	}

	if err := s.watchers.Add(t.ID, actor.UserID); err != nil {
		s.log.Error("ticket create: auto-watch by creator failed", "ticket_id", t.ID, "user_id", actor.UserID, "err", err)
	}
	metrics.MTTDSeconds.Observe(now.Sub(detectedAt).Seconds())
	s.logEvent(t.ID, actor, "ticket_created", map[string]any{"title": t.Title})
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

	// Stage-tracking overlay (DESIGN/03 §3.1.2b) - additive, doesn't gate the
	// transition above. Resolve stamps ResolvedAt; Reopen clears it (the
	// ticket is back in flight) and bumps ReopenCount, but deliberately
	// leaves AckedAt/MitigatedAt alone so the progress bar can reset to
	// whichever of those is the latest non-nil milestone.
	switch {
	case final == models.StatusResolved:
		now := time.Now()
		t.ResolvedAt = &now
		if t.MitigatedAt != nil {
			metrics.MTTRSeconds.Observe(now.Sub(*t.MitigatedAt).Seconds())
		}
	case action == ActionReopen:
		t.ResolvedAt = nil
		t.ReopenCount++
	}

	if err := s.tickets.Update(t); err != nil {
		return nil, err
	}

	cursor := from
	for _, st := range path {
		s.logEvent(t.ID, actor, "status_changed", map[string]any{
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

// Assign lets a Manager (or SystemAdmin acting via Sudo-as, DESIGN/02 §2.5)
// assign a ticket to any agent, bypassing queue membership. Gated at the
// route by RequireCapability(CapQueueOps) too - this is defense-in-depth per
// ARCHITECTURE.md's rule that RBAC belongs in service, not just middleware.
func (s *TicketService) Assign(actor *auth.Claims, ticketID, assigneeID int64) (*models.Ticket, error) {
	if !actor.Role.Can(models.CapQueueOps) {
		return nil, ErrForbidden
	}
	return s.assign(actor, ticketID, assigneeID, ActionAssign)
}

func (s *TicketService) assign(actor *auth.Claims, ticketID, assigneeID int64, action Action) (*models.Ticket, error) {
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return nil, err
	}

	// Engineers may only pick up/be assigned tickets in a queue they belong to
	// (e.g. an "Engineers" or "Networking" queue); Manager (or SystemAdmin via
	// Sudo-as) bypass this - see Assign's CapQueueOps check above.
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
	// Stage-tracking overlay (DESIGN/03 §3.1.2b): Ack is stamped once, the
	// first time someone takes ownership, and never overwritten - a later
	// reassignment isn't a new Ack.
	if t.AckedAt == nil {
		now := time.Now()
		t.AckedAt = &now
		if t.DetectedAt != nil {
			metrics.MTTASeconds.Observe(now.Sub(*t.DetectedAt).Seconds())
		}
	}
	if err := s.tickets.Update(t); err != nil {
		return nil, err
	}
	s.logEvent(t.ID, actor, "assigned", map[string]any{"assignee_id": assigneeID, "action": action})
	s.fanOut("ticket.assigned", t.ID, t)
	s.workflows.Trigger("field_updated", t.ID, map[string]any{"ticket": t, "field": "assignee_id"})
	return t, nil
}

// MarkMitigated stamps the stage-tracking overlay's Mitigate milestone
// (DESIGN/03 §3.1.2b) - a workaround is in place, root cause not yet fixed.
// Deliberately does NOT touch Status: this sits beside the state machine in
// statemachine.go, not inside it. Optionally posts a note in the same call
// (the "post mitigation note" action is one tap for both Engineer and Agent
// personas, DESIGN/08 §8.1/§8.5). Overwrites any previous MitigatedAt, which
// is what lets a second mitigation after a Reopen show as "just now" rather
// than stale.
func (s *TicketService) MarkMitigated(actor *auth.Claims, ticketID int64, note string) (*models.Ticket, error) {
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return nil, err
	}
	if t.Status != models.StatusInProgress {
		return nil, fmt.Errorf("%w: ticket must be In Progress to mark mitigated", ErrForbidden)
	}

	now := time.Now()
	t.MitigatedAt = &now
	if t.AckedAt != nil {
		metrics.MTTMSeconds.Observe(now.Sub(*t.AckedAt).Seconds())
	}
	if err := s.tickets.Update(t); err != nil {
		return nil, err
	}
	s.logEvent(t.ID, actor, "ticket_mitigated", map[string]any{"via_agent": actor.Role == models.RoleAgent})

	// Mirrors NoteService.Add's write + fan-out directly (TicketService only
	// holds repo.NoteRepo, not NoteService, so this stays a repo-level Create
	// rather than adding a cross-service dependency for one optional note).
	if note != "" {
		n := &models.Note{TicketID: ticketID, AuthorID: actor.UserID, Body: note, Internal: false}
		if err := s.notes.Create(n); err != nil {
			s.log.Error("mark mitigated: post note failed", "ticket_id", ticketID, "err", err)
		} else {
			s.fanOut("note.added.external", ticketID, n)
			s.workflows.Trigger("note_added", ticketID, map[string]any{"note": n})
		}
	}

	s.fanOut("ticket.mitigated", t.ID, t)
	s.workflows.Trigger("field_updated", t.ID, map[string]any{"ticket": t, "field": "mitigated_at"})
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
	s.logEvent(t.ID, actor, "field_updated", changed)
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
	s.logEvent(ticketID, actor, "label_added", map[string]any{"name": name, "kind": kind})
	s.fanOut("ticket.label_added", ticketID, map[string]any{"name": name, "kind": kind})
	return nil
}

func (s *TicketService) RemoveLabel(actor *auth.Claims, ticketID, tagID int64) error {
	if err := s.tags.DetachFromTicket(ticketID, tagID); err != nil {
		return err
	}
	s.logEvent(ticketID, actor, "label_removed", map[string]any{"tag_id": tagID})
	return nil
}

// logEvent attributes the event to actor's identity (the sudo target's, if
// this action happened during a Sudo-as session) and additionally stamps
// SudoByID when set, so the row is self-describing without reconstructing
// session boundaries (DESIGN/02 §2.5).
func (s *TicketService) logEvent(ticketID int64, actor *auth.Claims, event string, details map[string]any) {
	b, _ := json.Marshal(details)
	e := &models.EventLog{TicketID: &ticketID, ActorID: &actor.UserID, Event: event, Details: string(b), SudoByID: actor.SudoByID}
	if err := s.events.Append(e); err != nil {
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
