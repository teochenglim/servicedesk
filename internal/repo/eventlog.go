package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type EventLogRepo struct{ db *gorm.DB }

func NewEventLogRepo(db *gorm.DB) *EventLogRepo { return &EventLogRepo{db: db} }

func (r *EventLogRepo) Append(e *models.EventLog) error { return r.db.Create(e).Error }

func (r *EventLogRepo) ListForTicket(ticketID int64) ([]models.EventLog, error) {
	var es []models.EventLog
	err := r.db.Where("ticket_id = ?", ticketID).Order("created_at ASC").Find(&es).Error
	return es, err
}

// AuditFilter drives the ServiceDeskAdmin system audit log (DESIGN/08 §8.3):
// actor/event are substring matches, SudoOnly restricts to events that
// happened during a Sudo-as session.
type AuditFilter struct {
	ActorID  *int64
	Event    string
	SudoOnly bool
	Limit    int
}

func (r *EventLogRepo) ListAudit(f AuditFilter) ([]models.EventLog, error) {
	q := r.db.Model(&models.EventLog{})
	if f.ActorID != nil {
		q = q.Where("actor_id = ?", *f.ActorID)
	}
	if f.Event != "" {
		q = q.Where("event LIKE ?", "%"+f.Event+"%")
	}
	if f.SudoOnly {
		q = q.Where("sudo_by_id IS NOT NULL")
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit)
	}
	var es []models.EventLog
	err := q.Order("created_at DESC").Find(&es).Error
	return es, err
}
