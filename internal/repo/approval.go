package repo

import (
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type ApprovalRepo struct{ db *gorm.DB }

func NewApprovalRepo(db *gorm.DB) *ApprovalRepo { return &ApprovalRepo{db: db} }

func (r *ApprovalRepo) Create(a *models.Approval) error {
	a.Status = "pending"
	return r.db.Create(a).Error
}

func (r *ApprovalRepo) ListPendingForRole(role models.Role) ([]models.Approval, error) {
	var as []models.Approval
	err := r.db.Where("status = ? AND approver_role = ?", "pending", role).Order("created_at").Find(&as).Error
	return as, err
}

func (r *ApprovalRepo) ListForTicket(ticketID int64) ([]models.Approval, error) {
	var as []models.Approval
	err := r.db.Where("ticket_id = ?", ticketID).Order("step").Find(&as).Error
	return as, err
}

func (r *ApprovalRepo) Decide(id, deciderID int64, approve bool) error {
	status := "rejected"
	if approve {
		status = "approved"
	}
	now := time.Now()
	return r.db.Model(&models.Approval{}).Where("id = ?", id).Updates(map[string]any{
		"status": status, "decided_by": deciderID, "decided_at": &now,
	}).Error
}

func (r *ApprovalRepo) Get(id int64) (*models.Approval, error) {
	var a models.Approval
	if err := r.db.First(&a, id).Error; err != nil {
		return nil, err
	}
	return &a, nil
}
