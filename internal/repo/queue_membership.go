package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

// QueueMembershipRepo tracks which Engineers belong to which queue (e.g. an
// "Engineers" or "Networking" queue). Pickup/assign is restricted to queue
// members; QueueAdmin/SystemAdmin bypass this (see service.TicketService).
type QueueMembershipRepo struct{ db *gorm.DB }

func NewQueueMembershipRepo(db *gorm.DB) *QueueMembershipRepo { return &QueueMembershipRepo{db: db} }

func (r *QueueMembershipRepo) IsMember(queueID, userID int64) (bool, error) {
	var count int64
	err := r.db.Model(&models.QueueMembership{}).
		Where("queue_id = ? AND user_id = ?", queueID, userID).Count(&count).Error
	return count > 0, err
}

func (r *QueueMembershipRepo) Add(queueID, userID int64) error {
	m := models.QueueMembership{QueueID: queueID, UserID: userID}
	return r.db.Where(m).FirstOrCreate(&m).Error
}

func (r *QueueMembershipRepo) Remove(queueID, userID int64) error {
	return r.db.Where("queue_id = ? AND user_id = ?", queueID, userID).Delete(&models.QueueMembership{}).Error
}

func (r *QueueMembershipRepo) ListMembers(queueID int64) ([]models.User, error) {
	var users []models.User
	err := r.db.Joins("JOIN queue_memberships ON queue_memberships.user_id = users.id").
		Where("queue_memberships.queue_id = ?", queueID).
		Order("users.username").Find(&users).Error
	return users, err
}

func (r *QueueMembershipRepo) ListQueueIDsForUser(userID int64) ([]int64, error) {
	var ids []int64
	err := r.db.Model(&models.QueueMembership{}).Where("user_id = ?", userID).Pluck("queue_id", &ids).Error
	return ids, err
}
