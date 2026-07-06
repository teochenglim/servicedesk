package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type CustomFieldRepo struct{ db *gorm.DB }

func NewCustomFieldRepo(db *gorm.DB) *CustomFieldRepo { return &CustomFieldRepo{db: db} }

func (r *CustomFieldRepo) ListForCategory(category string) ([]models.CustomFieldDef, error) {
	var defs []models.CustomFieldDef
	err := r.db.Where("category = ?", category).Order("id").Find(&defs).Error
	return defs, err
}

func (r *CustomFieldRepo) List() ([]models.CustomFieldDef, error) {
	var defs []models.CustomFieldDef
	err := r.db.Order("category, id").Find(&defs).Error
	return defs, err
}

func (r *CustomFieldRepo) Create(d *models.CustomFieldDef) error { return r.db.Create(d).Error }

func (r *CustomFieldRepo) Delete(id int64) error {
	return r.db.Delete(&models.CustomFieldDef{}, id).Error
}
