package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type OrgRepo struct{ db *gorm.DB }

func NewOrgRepo(db *gorm.DB) *OrgRepo { return &OrgRepo{db: db} }

func (r *OrgRepo) List() ([]models.Organization, error) {
	var orgs []models.Organization
	err := r.db.Order("name").Find(&orgs).Error
	return orgs, err
}

func (r *OrgRepo) Get(id int64) (*models.Organization, error) {
	var o models.Organization
	if err := r.db.First(&o, id).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OrgRepo) GetByName(name string) (*models.Organization, error) {
	var o models.Organization
	if err := r.db.Where("name = ?", name).First(&o).Error; err != nil {
		return nil, err
	}
	return &o, nil
}

func (r *OrgRepo) Create(o *models.Organization) error { return r.db.Create(o).Error }
