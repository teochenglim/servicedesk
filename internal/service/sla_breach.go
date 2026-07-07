package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// SLABreachChecker is the background poller that closes RELEASE/v_2.0.0.md's
// one real remaining gap: every other webhook event fires on a state
// transition someone triggers, but an SLA breach is time-based - nothing
// calls a handler when the clock just runs out. Mirrors webhook.Dispatcher's
// Run/ProcessOne polling shape.
type SLABreachChecker struct {
	tickets  *repo.TicketRepo
	events   *repo.EventLogRepo
	notifier EventPublisher
	webhooks WebhookDispatcher
	log      *slog.Logger
}

func NewSLABreachChecker(tickets *repo.TicketRepo, events *repo.EventLogRepo, notifier EventPublisher, webhooks WebhookDispatcher, log *slog.Logger) *SLABreachChecker {
	return &SLABreachChecker{tickets: tickets, events: events, notifier: notifier, webhooks: webhooks, log: log}
}

// Run polls for newly-breached tickets until ctx is cancelled.
func (c *SLABreachChecker) Run(ctx context.Context, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for c.ProcessOne() {
			}
		}
	}
}

// ProcessOne claims and alerts on one newly-breached ticket, reporting
// whether one was found so Run can drain the backlog before sleeping again.
func (c *SLABreachChecker) ProcessOne() bool {
	t, err := c.tickets.ClaimNextBreach(time.Now())
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			c.log.Error("sla breach: claim failed", "err", err)
		}
		return false
	}

	actorID := auth.SystemActorID
	if err := c.events.Append(&models.EventLog{TicketID: &t.ID, ActorID: &actorID, Event: "sla_breached", Details: "{}"}); err != nil {
		c.log.Error("sla breach: audit log write failed", "ticket_id", t.ID, "err", err)
	}
	if c.notifier != nil {
		c.notifier.Publish(t.ID, "ticket.sla_breached", t)
	}
	if c.webhooks != nil {
		c.webhooks.Dispatch("ticket.sla_breached", t)
	}
	return true
}
