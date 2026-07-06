package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/repo"
)

const maxAttempts = 3 // DESIGN.md 3.6: exponential backoff, max 3 retries

// Dispatcher implements service.WebhookDispatcher and runs the durable outbox
// worker that actually delivers queued webhook payloads with retry/backoff.
type Dispatcher struct {
	webhooks   *repo.WebhookRepo
	deliveries *repo.WebhookDeliveryRepo
	client     *http.Client
	log        *slog.Logger
}

func NewDispatcher(webhooks *repo.WebhookRepo, deliveries *repo.WebhookDeliveryRepo, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		webhooks:   webhooks,
		deliveries: deliveries,
		client:     &http.Client{Timeout: 10 * time.Second},
		log:        log,
	}
}

// Dispatch enqueues a delivery for every active webhook subscribed to event.
func (d *Dispatcher) Dispatch(event string, payload any) {
	hooks, err := d.webhooks.ListActiveForEvent(event)
	if err != nil {
		d.log.Error("webhook: list active failed", "err", err)
		return
	}
	body, _ := json.Marshal(map[string]any{
		"event":     event,
		"payload":   payload,
		"timestamp": time.Now().UTC(),
	})
	for _, h := range hooks {
		if err := d.deliveries.Enqueue(h.ID, event, string(body)); err != nil {
			d.log.Error("webhook: enqueue failed", "webhook_id", h.ID, "err", err)
		}
	}
}

// Run polls the outbox until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context, pollInterval time.Duration) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for d.ProcessOne() {
			}
		}
	}
}

// ProcessOne delivers a single due webhook and reports whether one was found,
// so Run can drain the backlog before sleeping again.
func (d *Dispatcher) ProcessOne() bool {
	delivery, err := d.deliveries.ClaimNext()
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			d.log.Error("webhook: claim failed", "err", err)
		}
		return false
	}

	hook, err := d.webhooks.Get(delivery.WebhookID)
	if err != nil {
		if markErr := d.deliveries.MarkFailed(delivery.ID, "webhook config missing", time.Now(), true); markErr != nil {
			d.log.Error("webhook: mark-failed write failed", "delivery_id", delivery.ID, "err", markErr)
		}
		return true
	}

	req, err := http.NewRequest(http.MethodPost, hook.URL, bytes.NewReader([]byte(delivery.Payload)))
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-ServiceDesk-Event", delivery.Event)
		if hook.Secret != "" {
			req.Header.Set("X-ServiceDesk-Signature", sign(hook.Secret, delivery.Payload))
		}
	}

	var resp *http.Response
	if err == nil {
		resp, err = d.client.Do(req)
	}
	if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		resp.Body.Close()
		if markErr := d.deliveries.MarkDelivered(delivery.ID); markErr != nil {
			d.log.Error("webhook: mark-delivered write failed", "delivery_id", delivery.ID, "err", markErr)
		}
		return true
	}
	if resp != nil {
		resp.Body.Close()
	}

	msg := "request failed"
	if err != nil {
		msg = err.Error()
	} else {
		msg = "non-2xx status " + resp.Status
	}
	giveUp := delivery.Attempts >= maxAttempts
	backoff := time.Duration(1<<delivery.Attempts) * time.Second // 2s, 4s, 8s
	if markErr := d.deliveries.MarkFailed(delivery.ID, msg, time.Now().Add(backoff), giveUp); markErr != nil {
		d.log.Error("webhook: mark-failed write failed", "delivery_id", delivery.ID, "err", markErr)
	}
	d.log.Warn("webhook: delivery failed", "webhook_id", hook.ID, "attempt", delivery.Attempts, "err", msg, "give_up", giveUp)
	return true
}

func sign(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}
