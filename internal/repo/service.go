package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

// ServiceRepo is the business-service catalog (RELEASE/v_2.1.0.md) - thin
// wrapper, same shape as QueueRepo (plain hard delete, no reference-guard).
type ServiceRepo struct{ db *gorm.DB }

func NewServiceRepo(db *gorm.DB) *ServiceRepo { return &ServiceRepo{db: db} }

func (r *ServiceRepo) List() ([]models.Service, error) {
	var ss []models.Service
	err := r.db.Order("name").Find(&ss).Error
	return ss, err
}

func (r *ServiceRepo) Get(id int64) (*models.Service, error) {
	var s models.Service
	if err := r.db.First(&s, id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *ServiceRepo) Create(s *models.Service) error {
	return r.db.Create(s).Error
}

func (r *ServiceRepo) Update(s *models.Service) error {
	return r.db.Save(s).Error
}

func (r *ServiceRepo) Delete(id int64) error {
	return r.db.Delete(&models.Service{}, id).Error
}
