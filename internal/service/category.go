package service

import (
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// CategoryService is the ticket-submission category catalog CRUD
// (RELEASE/v_3.0.5.md) - no state machine or RBAC beyond the route-level
// SystemAdmin gate, so this is a thin pass-through, same shape as
// ServiceCatalogService/ProblemService.
type CategoryService struct {
	categories *repo.CategoryRepo
}

func NewCategoryService(categories *repo.CategoryRepo) *CategoryService {
	return &CategoryService{categories: categories}
}

func (s *CategoryService) Create(c *models.Category) error        { return s.categories.Create(c) }
func (s *CategoryService) Update(c *models.Category) error        { return s.categories.Update(c) }
func (s *CategoryService) Get(id int64) (*models.Category, error) { return s.categories.Get(id) }
func (s *CategoryService) List() ([]models.Category, error)       { return s.categories.List() }
func (s *CategoryService) ListTopLevel() ([]models.Category, error) {
	return s.categories.ListTopLevel()
}
func (s *CategoryService) Delete(id int64) error { return s.categories.Delete(id) }
