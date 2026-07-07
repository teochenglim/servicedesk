package service

import (
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// ServiceCatalogService is the business-service catalog CRUD
// (RELEASE/v_2.1.0.md) - no state machine or RBAC beyond the route-level
// SystemAdmin gate, so this is a thin pass-through, same shape as ProblemService.
type ServiceCatalogService struct {
	services *repo.ServiceRepo
}

func NewServiceCatalogService(services *repo.ServiceRepo) *ServiceCatalogService {
	return &ServiceCatalogService{services: services}
}

func (s *ServiceCatalogService) Create(svc *models.Service) error      { return s.services.Create(svc) }
func (s *ServiceCatalogService) Update(svc *models.Service) error      { return s.services.Update(svc) }
func (s *ServiceCatalogService) Get(id int64) (*models.Service, error) { return s.services.Get(id) }
func (s *ServiceCatalogService) List() ([]models.Service, error)       { return s.services.List() }
func (s *ServiceCatalogService) Delete(id int64) error                 { return s.services.Delete(id) }
