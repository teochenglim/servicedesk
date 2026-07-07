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
//
// The candidate is found with a plain SELECT, but the actual claim is a
// second UPDATE that re-checks "status = pending" by the candidate's
// specific ID and inspects RowsAffected, rather than blindly updating by ID:
// cmd/servicedesk/main.go runs WorkerPoolSize (default 4) copies of the
// engine concurrently, and a select-then-blind-update let every worker's
// SELECT see the same still-pending task before any UPDATE committed, so
// more than one could believe it won the claim and run the same runbook
// step twice (see RELEASE/v_2.0.0.md - the identical race was confirmed live
// in WebhookDeliveryRepo.ClaimNext, which this mirrors). Re-checking the
// exact status in the guarded UPDATE means only the first worker to actually
// execute it affects a row; the rest see 0 rows affected and back off.
func (r *WorkflowTaskRepo) ClaimNext() (*models.WorkflowTask, error) {
	var t models.WorkflowTask
	if err := r.db.Where("status = ? AND next_run_at <= ?", models.TaskPending, time.Now().Unix()).
		Order("id").First(&t).Error; err != nil {
		return nil, err
	}
	res := r.db.Model(&models.WorkflowTask{}).
		Where("id = ? AND status = ?", t.ID, models.TaskPending).
		Updates(map[string]any{"status": models.TaskProcessing, "updated_at": time.Now()})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound // another worker claimed it first
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
