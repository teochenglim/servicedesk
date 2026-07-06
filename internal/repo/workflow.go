package repo

import (
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type WorkflowRepo struct{ db *gorm.DB }

func NewWorkflowRepo(db *gorm.DB) *WorkflowRepo { return &WorkflowRepo{db: db} }

func (r *WorkflowRepo) Create(w *models.Workflow) error { return r.db.Create(w).Error }

func (r *WorkflowRepo) List() ([]models.Workflow, error) {
	var ws []models.Workflow
	err := r.db.Order("id").Find(&ws).Error
	return ws, err
}

func (r *WorkflowRepo) Get(id int64) (*models.Workflow, error) {
	var w models.Workflow
	if err := r.db.First(&w, id).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *WorkflowRepo) ListActiveForTrigger(trigger string) ([]models.Workflow, error) {
	var ws []models.Workflow
	err := r.db.Where("trigger_event = ? AND active = ?", trigger, true).Find(&ws).Error
	return ws, err
}

type WorkflowTaskRepo struct{ db *gorm.DB }

func NewWorkflowTaskRepo(db *gorm.DB) *WorkflowTaskRepo { return &WorkflowTaskRepo{db: db} }

func (r *WorkflowTaskRepo) Create(t *models.WorkflowTask) error { return r.db.Create(t).Error }

func (r *WorkflowTaskRepo) Get(id int64) (*models.WorkflowTask, error) {
	var t models.WorkflowTask
	if err := r.db.First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// ClaimNext grabs the oldest due pending task and marks it processing.
func (r *WorkflowTaskRepo) ClaimNext() (*models.WorkflowTask, error) {
	var t models.WorkflowTask
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("status = ? AND next_run_at <= ?", models.TaskPending, time.Now().Unix()).
			Order("id").First(&t).Error; err != nil {
			return err
		}
		return tx.Model(&t).Updates(map[string]any{"status": models.TaskProcessing, "updated_at": time.Now()}).Error
	})
	if err != nil {
		return nil, err
	}
	t.Status = models.TaskProcessing
	return &t, nil
}

func (r *WorkflowTaskRepo) Save(t *models.WorkflowTask) error {
	return r.db.Save(t).Error
}

func (r *WorkflowTaskRepo) ListWaitingForTicket(ticketID int64) ([]models.WorkflowTask, error) {
	var ts []models.WorkflowTask
	err := r.db.Where("ticket_id = ? AND status = ?", ticketID, models.TaskWaitingUser).Order("id").Find(&ts).Error
	return ts, err
}
