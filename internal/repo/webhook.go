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

// claimLeaseSeconds must exceed webhook.Dispatcher's http.Client timeout
// (10s): unlike WorkflowTask, WebhookDelivery has no real "in-flight" status
// to transition to (only pending|delivered|failed), so ClaimNext instead
// pushes NextRunAt forward by this lease while a delivery attempt is live -
// otherwise the row stays "due" (status=pending, next_run_at<=now) for the
// entire multi-second HTTP attempt, and every poll tick from every other
// worker re-claims it too. Confirmed via live smoke test: even after adding
// the RowsAffected-guarded UPDATE below (which only closes the sub-instant
// race), the same delivery still went out 4 times, one per pool worker,
// because each one re-claimed it on a later tick while the first was still
// waiting on the HTTP response. If a worker crashes mid-delivery without
// calling MarkDelivered/MarkFailed, the lease simply expires and another
// worker picks it up - the same at-least-once retry behavior this queue
// already provides on real failures.
const claimLeaseSeconds = 30

// ClaimNext atomically grabs one due delivery, bumps its attempt counter,
// and pushes its NextRunAt out by claimLeaseSeconds for the duration of the
// attempt (see claimLeaseSeconds's comment for why that's needed on top of
// the guard below).
//
// The candidate is found with a plain SELECT, but the actual claim is a
// second UPDATE that re-checks "status = pending AND next_run_at <= now" by
// the candidate's specific ID and inspects RowsAffected: cmd/servicedesk/main.go
// runs WorkerPoolSize (default 4) copies of the dispatcher concurrently, and
// a select-then-blind-update-by-id (the previous shape here) let every
// worker's SELECT see the same still-eligible row before any UPDATE
// committed, so all of them believed they'd won the claim - confirmed via
// live smoke test (RELEASE/v_2.0.0.md's SLA-breach work), which delivered the
// same webhook multiple times under the default pool size. Re-checking the
// exact eligibility condition in the guarded UPDATE means only the first
// worker to actually execute it affects a row; the rest see 0 rows affected
// and correctly back off instead of redelivering.
func (r *WebhookDeliveryRepo) ClaimNext() (*models.WebhookDelivery, error) {
	var d models.WebhookDelivery
	now := time.Now()
	if err := r.db.Where("status = ? AND next_run_at <= ?", "pending", now.Unix()).
		Order("id").First(&d).Error; err != nil {
		return nil, err
	}
	res := r.db.Model(&models.WebhookDelivery{}).
		Where("id = ? AND status = ? AND next_run_at <= ?", d.ID, "pending", now.Unix()).
		Updates(map[string]any{
			"attempts":    gorm.Expr("attempts + 1"),
			"next_run_at": now.Add(claimLeaseSeconds * time.Second).Unix(),
		})
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound // another worker claimed it first
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
