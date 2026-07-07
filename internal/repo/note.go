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

func (r *NoteRepo) Get(id int64) (*models.Note, error) {
	var n models.Note
	if err := r.db.First(&n, id).Error; err != nil {
		return nil, err
	}
	return &n, nil
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

// ListForTickets returns every note across the given tickets, oldest first,
// so a caller can reduce it to "latest note per ticket" (the Manager Activity
// List's "last message" preview, DESIGN/08 §8.6) without one query per ticket.
func (r *NoteRepo) ListForTickets(ticketIDs []int64) ([]models.Note, error) {
	if len(ticketIDs) == 0 {
		return nil, nil
	}
	var notes []models.Note
	err := r.db.Where("ticket_id IN ?", ticketIDs).Order("created_at ASC").Find(&notes).Error
	return notes, err
}
