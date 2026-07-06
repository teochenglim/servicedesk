package repo

import (
	"strings"
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type WebhookRepo struct{ db *gorm.DB }

func NewWebhookRepo(db *gorm.DB) *WebhookRepo { return &WebhookRepo{db: db} }

func (r *WebhookRepo) Create(w *models.Webhook) error { return r.db.Create(w).Error }

func (r *WebhookRepo) Get(id int64) (*models.Webhook, error) {
	var w models.Webhook
	if err := r.db.First(&w, id).Error; err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *WebhookRepo) List() ([]models.Webhook, error) {
	var ws []models.Webhook
	err := r.db.Order("id").Find(&ws).Error
	return ws, err
}

func (r *WebhookRepo) ListActiveForEvent(event string) ([]models.Webhook, error) {
	all, err := r.List()
	if err != nil {
		return nil, err
	}
	var out []models.Webhook
	for _, w := range all {
		if !w.Active {
			continue
		}
		for _, e := range strings.Split(w.Events, ",") {
			if strings.TrimSpace(e) == event || strings.TrimSpace(e) == "*" {
				out = append(out, w)
				break
			}
		}
	}
	return out, nil
}

func (r *WebhookRepo) Delete(id int64) error {
	return r.db.Delete(&models.Webhook{}, id).Error
}

type WebhookDeliveryRepo struct{ db *gorm.DB }

func NewWebhookDeliveryRepo(db *gorm.DB) *WebhookDeliveryRepo { return &WebhookDeliveryRepo{db: db} }

func (r *WebhookDeliveryRepo) Enqueue(webhookID int64, event, payload string) error {
	d := models.WebhookDelivery{
		WebhookID: webhookID, Event: event, Payload: payload,
		Status: "pending", NextRunAt: time.Now().Unix(),
	}
	return r.db.Create(&d).Error
}

// ClaimNext atomically grabs one due delivery and bumps its attempt counter.
func (r *WebhookDeliveryRepo) ClaimNext() (*models.WebhookDelivery, error) {
	var d models.WebhookDelivery
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("status = ? AND next_run_at <= ?", "pending", time.Now().Unix()).
			Order("id").First(&d).Error; err != nil {
			return err
		}
		return tx.Model(&d).Update("attempts", gorm.Expr("attempts + 1")).Error
	})
	if err != nil {
		return nil, err
	}
	d.Attempts++
	return &d, nil
}

func (r *WebhookDeliveryRepo) MarkDelivered(id int64) error {
	return r.db.Model(&models.WebhookDelivery{}).Where("id = ?", id).Update("status", "delivered").Error
}

func (r *WebhookDeliveryRepo) MarkFailed(id int64, errMsg string, nextRunAt time.Time, giveUp bool) error {
	status := "pending"
	if giveUp {
		status = "failed"
	}
	return r.db.Model(&models.WebhookDelivery{}).Where("id = ?", id).Updates(map[string]any{
		"status": status, "last_error": errMsg, "next_run_at": nextRunAt.Unix(),
	}).Error
}
