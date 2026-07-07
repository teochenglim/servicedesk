package service

import (
	"encoding/json"
	"time"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
	"servicedesk/internal/sla"
)

// slaTableFor parses a queue's SLATargets JSON blob into an sla.Table; an
// empty/invalid blob just yields an empty Table, which sla.Evaluate already
// falls back to sla.DefaultTable for.
func slaTableFor(q *models.Queue) sla.Table {
	var t sla.Table
	if q.SLATargets != "" {
		_ = json.Unmarshal([]byte(q.SLATargets), &t)
	}
	return t
}

// slaDueAt computes a ticket's SLA due time at creation, from its queue's
// configured SLA table (falling back to sla.DefaultTable per priority).
func slaDueAt(q *models.Queue, priority models.Priority, category string, from time.Time) *time.Time {
	return slaTableFor(q).DueAt(string(priority), category, from)
}

// QueueService adds SLA-target configuration on top of the existing bare
// repo.QueueRepo CRUD (DESIGN/08 §8.6: this is the point queue mutations
// graduate from repo-only into real business logic, per RELEASE/v_2.0.0.md's
// still-open "SLA due-date automation" item). The rule table itself is
// evaluated by internal/sla, not here - this is just persistence/RBAC glue.
type QueueService struct {
	queues *repo.QueueRepo
}

func NewQueueService(queues *repo.QueueRepo) *QueueService {
	return &QueueService{queues: queues}
}

// SLATable returns a queue's configured SLA rules, for rendering the edit form.
func (s *QueueService) SLATable(queueID int64) (sla.Table, error) {
	q, err := s.queues.Get(queueID)
	if err != nil {
		return nil, err
	}
	return slaTableFor(q), nil
}

// SetSLATable overwrites a queue's SLA rules. Capability-gated the same way
// as the rest of queue ownership (DESIGN/02 §2.1.1) - checked here too, not
// just at the route, per ARCHITECTURE.md's defense-in-depth rule.
func (s *QueueService) SetSLATable(actor *auth.Claims, queueID int64, table sla.Table) error {
	if !actor.Role.Can(models.CapQueueOps) {
		return ErrForbidden
	}
	q, err := s.queues.Get(queueID)
	if err != nil {
		return err
	}
	blob, err := json.Marshal(table)
	if err != nil {
		return err
	}
	q.SLATargets = string(blob)
	return s.queues.Update(q)
}
