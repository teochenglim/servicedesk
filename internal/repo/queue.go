package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type QueueRepo struct{ db *gorm.DB }

func NewQueueRepo(db *gorm.DB) *QueueRepo { return &QueueRepo{db: db} }

func (r *QueueRepo) List() ([]models.Queue, error) {
	var qs []models.Queue
	err := r.db.Order("name").Find(&qs).Error
	return qs, err
}

func (r *QueueRepo) Get(id int64) (*models.Queue, error) {
	var q models.Queue
	if err := r.db.First(&q, id).Error; err != nil {
		return nil, err
	}
	return &q, nil
}

func (r *QueueRepo) Create(q *models.Queue) error {
	return r.db.Create(q).Error
}

func (r *QueueRepo) Update(q *models.Queue) error {
	return r.db.Save(q).Error
}

func (r *QueueRepo) Delete(id int64) error {
	return r.db.Delete(&models.Queue{}, id).Error
}
