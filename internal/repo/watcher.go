package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type WatcherRepo struct{ db *gorm.DB }

func NewWatcherRepo(db *gorm.DB) *WatcherRepo { return &WatcherRepo{db: db} }

func (r *WatcherRepo) Add(ticketID, userID int64) error {
	m := models.Watcher{TicketID: ticketID, UserID: userID}
	return r.db.Where(m).FirstOrCreate(&m).Error
}

func (r *WatcherRepo) Remove(ticketID, userID int64) error {
	return r.db.Where("ticket_id = ? AND user_id = ?", ticketID, userID).Delete(&models.Watcher{}).Error
}

func (r *WatcherRepo) IsWatching(ticketID, userID int64) (bool, error) {
	var count int64
	err := r.db.Model(&models.Watcher{}).Where("ticket_id = ? AND user_id = ?", ticketID, userID).Count(&count).Error
	return count > 0, err
}

func (r *WatcherRepo) ListUserIDsForTicket(ticketID int64) ([]int64, error) {
	var ids []int64
	err := r.db.Model(&models.Watcher{}).Where("ticket_id = ?", ticketID).Pluck("user_id", &ids).Error
	return ids, err
}
