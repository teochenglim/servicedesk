package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type AISnapshotRepo struct{ db *gorm.DB }

func NewAISnapshotRepo(db *gorm.DB) *AISnapshotRepo { return &AISnapshotRepo{db: db} }

func (r *AISnapshotRepo) Create(s *models.TicketAISnapshot) error {
	return r.db.Create(s).Error
}

// Latest returns the most recent snapshot for a ticket, or gorm.ErrRecordNotFound
// if the panel has never been generated for it yet.
func (r *AISnapshotRepo) Latest(ticketID int64) (*models.TicketAISnapshot, error) {
	var s models.TicketAISnapshot
	if err := r.db.Where("ticket_id = ?", ticketID).Order("id DESC").First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

// ListForTicket returns every generation, oldest first - the raw
// (history -> extraction) dataset DESIGN/08 §8.9 calls out as useful for
// future fine-tuning/evaluation, even though only the latest is ever rendered.
func (r *AISnapshotRepo) ListForTicket(ticketID int64) ([]models.TicketAISnapshot, error) {
	var ss []models.TicketAISnapshot
	err := r.db.Where("ticket_id = ?", ticketID).Order("id ASC").Find(&ss).Error
	return ss, err
}
