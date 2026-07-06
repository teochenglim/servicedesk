package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type TagRepo struct{ db *gorm.DB }

func NewTagRepo(db *gorm.DB) *TagRepo { return &TagRepo{db: db} }

func (r *TagRepo) List(kind string) ([]models.Tag, error) {
	q := r.db.Model(&models.Tag{})
	if kind != "" {
		q = q.Where("kind = ?", kind)
	}
	var tags []models.Tag
	err := q.Order("name").Find(&tags).Error
	return tags, err
}

func (r *TagRepo) GetOrCreate(name, kind string) (*models.Tag, error) {
	t := models.Tag{Name: name, Kind: kind}
	err := r.db.Where(models.Tag{Name: name, Kind: kind}).FirstOrCreate(&t).Error
	return &t, err
}

func (r *TagRepo) AttachToTicket(ticketID, tagID int64) error {
	m := models.TicketTag{TicketID: ticketID, TagID: tagID}
	return r.db.Where(m).FirstOrCreate(&m).Error
}

func (r *TagRepo) DetachFromTicket(ticketID, tagID int64) error {
	return r.db.Where("ticket_id = ? AND tag_id = ?", ticketID, tagID).Delete(&models.TicketTag{}).Error
}

func (r *TagRepo) ListForTicket(ticketID int64) ([]models.Tag, error) {
	var tags []models.Tag
	err := r.db.Joins("JOIN ticket_tags ON ticket_tags.tag_id = tags.id").
		Where("ticket_tags.ticket_id = ?", ticketID).
		Order("tags.kind, tags.name").Find(&tags).Error
	return tags, err
}
