package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

// CategoryRepo is the ticket-submission category catalog (RELEASE/v_3.0.5.md) -
// thin wrapper, same shape as ServiceRepo (plain hard delete, no
// reference-guard).
type CategoryRepo struct{ db *gorm.DB }

func NewCategoryRepo(db *gorm.DB) *CategoryRepo { return &CategoryRepo{db: db} }

func (r *CategoryRepo) List() ([]models.Category, error) {
	var cs []models.Category
	err := r.db.Order("name").Find(&cs).Error
	return cs, err
}

// ListTopLevel returns only ParentID == nil categories - the ticket
// submission form's dropdown only ever shows the top level, even though the
// schema is ready for the requested 3-layer nesting.
func (r *CategoryRepo) ListTopLevel() ([]models.Category, error) {
	var cs []models.Category
	err := r.db.Where("parent_id IS NULL").Order("name").Find(&cs).Error
	return cs, err
}

func (r *CategoryRepo) Get(id int64) (*models.Category, error) {
	var c models.Category
	if err := r.db.First(&c, id).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *CategoryRepo) Create(c *models.Category) error {
	return r.db.Create(c).Error
}

func (r *CategoryRepo) Update(c *models.Category) error {
	return r.db.Save(c).Error
}

func (r *CategoryRepo) Delete(id int64) error {
	return r.db.Delete(&models.Category{}, id).Error
}
