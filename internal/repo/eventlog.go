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
