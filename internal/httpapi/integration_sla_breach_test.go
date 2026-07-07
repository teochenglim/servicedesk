package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"servicedesk/internal/models"
)

// TestSLABreach_DeliversWebhookExactlyOnce covers RELEASE/v_2.0.0.md's one
// real remaining gap: SLA breach is time-based, not triggered by a state
// transition someone performs, so it needs the background
// SLABreachChecker/webhook.Dispatcher pair to actually alert.
func TestSLABreach_DeliversWebhookExactlyOnce(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Overdue ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"x"},
	})

	// Force the ticket's SLADueAt into the past - normally only reachable by
	// waiting out a real SLA window.
	past := time.Now().Add(-time.Hour)
	if err := env.db.Model(&models.Ticket{}).Where("id = ?", 1).Update("sla_due_at", past).Error; err != nil {
		t.Fatalf("force SLADueAt into the past: %v", err)
	}

	received := make(chan string, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		received <- string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	admin.mustPost(t, "/admin/webhooks", url.Values{
		"url": {receiver.URL}, "events": {"ticket.sla_breached"}, "secret": {"whsec-test"},
	})

	if !env.slaBreachChecker.ProcessOne() {
		t.Fatal("expected the overdue ticket to be claimed as a new breach")
	}
	env.whDispatcher.ProcessOne()

	select {
	case payload := <-received:
		if !strings.Contains(payload, "Overdue ticket") {
			t.Fatalf("payload missing ticket data: %s", payload)
		}
	default:
		t.Fatal("webhook receiver never got a delivery")
	}

	// A second poll must not re-claim (and re-alert on) the same ticket.
	if env.slaBreachChecker.ProcessOne() {
		t.Fatal("expected no further breach to claim - already notified")
	}

	tk := getTicket(t, env, 1)
	if tk.SLABreachNotifiedAt == nil {
		t.Error("expected SLABreachNotifiedAt to be stamped")
	}
}

// TestSLABreach_ReopenAllowsARepeatAlert covers TicketService.Transition
// clearing SLABreachNotifiedAt on reopen, so a ticket that breaches again
// after being reopened is alerted on again rather than staying silently suppressed.
func TestSLABreach_ReopenAllowsARepeatAlert(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Will be reopened"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"x"},
	})
	past := time.Now().Add(-time.Hour)
	env.db.Model(&models.Ticket{}).Where("id = ?", 1).Update("sla_due_at", past)

	if !env.slaBreachChecker.ProcessOne() {
		t.Fatal("expected the first claim to succeed")
	}

	// Walk it to Resolved -> Closed -> Reopen (only path that clears the flag).
	admin.mustPost(t, "/tickets/1/pickup", nil)
	admin.mustPost(t, "/tickets/1/transition", url.Values{"action": {"resolve"}})
	admin.mustPost(t, "/tickets/1/transition", url.Values{"action": {"confirm"}})
	admin.mustPost(t, "/tickets/1/transition", url.Values{"action": {"reopen"}})

	if !env.slaBreachChecker.ProcessOne() {
		t.Fatal("expected the reopened (still overdue) ticket to be claimable again")
	}
}
