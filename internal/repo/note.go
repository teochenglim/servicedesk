package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type NoteRepo struct{ db *gorm.DB }

func NewNoteRepo(db *gorm.DB) *NoteRepo { return &NoteRepo{db: db} }

func (r *NoteRepo) Create(n *models.Note) error {
	return r.db.Create(n).Error
}

// ListByTicket returns notes for a ticket; includeInternal gates internal notes for Customer role.
func (r *NoteRepo) ListByTicket(ticketID int64, includeInternal bool) ([]models.Note, error) {
	q := r.db.Where("ticket_id = ?", ticketID)
	if !includeInternal {
		q = q.Where("internal = ?", false)
	}
	var notes []models.Note
	err := q.Order("created_at ASC").Find(&notes).Error
	return notes, err
}
