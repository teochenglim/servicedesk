package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type AttachmentRepo struct{ db *gorm.DB }

func NewAttachmentRepo(db *gorm.DB) *AttachmentRepo { return &AttachmentRepo{db: db} }

func (r *AttachmentRepo) Create(a *models.Attachment) error {
	return r.db.Create(a).Error
}

// Get loads a full row, including its Data blob - only for the download path.
func (r *AttachmentRepo) Get(id int64) (*models.Attachment, error) {
	var a models.Attachment
	if err := r.db.First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}

// ListMetaForTicket returns every attachment on a ticket (both ticket-level
// and note-level) without loading their Data blobs - list/thread rendering
// only needs the metadata to build a thumbnail/download link. Returns
// everything regardless of visibility; filtering per-viewer (Internal,
// CustomerPrivate) is service.AttachmentService.FilterVisible's job, not the
// repo's - keeps this layer a dumb fetch per ARCHITECTURE.md's layering rule.
func (r *AttachmentRepo) ListMetaForTicket(ticketID int64) ([]models.Attachment, error) {
	var atts []models.Attachment
	err := r.db.Select("id, ticket_id, note_id, uploader_id, filename, mime_type, size_bytes, internal, customer_private, storage_backend, created_at").
		Where("ticket_id = ?", ticketID).Order("created_at ASC").Find(&atts).Error
	return atts, err
}
