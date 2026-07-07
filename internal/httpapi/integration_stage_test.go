package httpapi

import (
	"net/url"
	"strings"
	"testing"

	"servicedesk/internal/models"
)

func getTicket(t *testing.T, env *testEnv, id int64) models.Ticket {
	t.Helper()
	var tk models.Ticket
	if err := env.db.First(&tk, id).Error; err != nil {
		t.Fatalf("load ticket %d: %v", id, err)
	}
	return tk
}

// TestTicket_StageTrackingOverlay walks create -> pickup -> mitigate ->
// resolve -> reopen -> mitigate -> resolve, asserting the additive stage
// timestamps at each step without ever touching the underlying Status state
// machine's own test coverage (statemachine_test.go covers that separately).
func TestTicket_StageTrackingOverlay(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	qadmin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}}) // eng1

	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Disk full"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"infra"},
	})

	tk := getTicket(t, env, 1)
	if tk.DetectedAt == nil {
		t.Fatal("DetectedAt should default to creation time")
	}
	if tk.AckedAt != nil || tk.MitigatedAt != nil || tk.ResolvedAt != nil || tk.ReopenCount != 0 {
		t.Fatalf("new ticket should have no stage stamps yet: %+v", tk)
	}

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")
	eng.mustPost(t, "/tickets/1/pickup", nil)

	tk = getTicket(t, env, 1)
	if tk.AckedAt == nil {
		t.Fatal("AckedAt should be stamped after pickup")
	}
	firstAck := *tk.AckedAt

	eng.mustPost(t, "/tickets/1/mitigate", url.Values{"note": {"applied workaround"}})
	tk = getTicket(t, env, 1)
	if tk.MitigatedAt == nil {
		t.Fatal("MitigatedAt should be stamped after mark-mitigated")
	}
	if !tk.AckedAt.Equal(firstAck) {
		t.Fatal("AckedAt should be untouched by mark-mitigated")
	}
	body := bodyString(t, eng.get("/tickets/1"))
	if !strings.Contains(body, "applied workaround") {
		t.Fatal("mark-mitigated should have posted the note into the thread")
	}

	eng.mustPost(t, "/tickets/1/transition", url.Values{"action": {"resolve"}})
	tk = getTicket(t, env, 1)
	if tk.ResolvedAt == nil {
		t.Fatal("ResolvedAt should be stamped after resolve")
	}

	// Reopen (as the original creator, admin) clears ResolvedAt and bumps
	// ReopenCount, but must leave AckedAt/MitigatedAt alone so the bar resets
	// to the Mitigate dot rather than losing history.
	admin.mustPost(t, "/tickets/1/transition", url.Values{"action": {"reopen"}})
	tk = getTicket(t, env, 1)
	if tk.ResolvedAt != nil {
		t.Fatal("ResolvedAt should be cleared after reopen")
	}
	if tk.ReopenCount != 1 {
		t.Fatalf("ReopenCount = %d, want 1", tk.ReopenCount)
	}
	if tk.MitigatedAt == nil || !tk.AckedAt.Equal(firstAck) {
		t.Fatal("AckedAt/MitigatedAt should survive a reopen unchanged")
	}

	// Mitigate again: MitigatedAt should be overwritten with a fresh timestamp.
	prevMitigatedAt := *tk.MitigatedAt
	eng.mustPost(t, "/tickets/1/mitigate", url.Values{})
	tk = getTicket(t, env, 1)
	if tk.MitigatedAt == nil || !tk.MitigatedAt.After(prevMitigatedAt) {
		t.Fatal("MitigatedAt should be overwritten by a second mark-mitigated after reopen")
	}

	eng.mustPost(t, "/tickets/1/transition", url.Values{"action": {"resolve"}})
	tk = getTicket(t, env, 1)
	if tk.ResolvedAt == nil {
		t.Fatal("ResolvedAt should be re-stamped on second resolve")
	}
}

// TestTicket_RejectedHasNoStageBar confirms Rejected sits outside the
// Detect/Ack/Mitigate/Resolve overlay entirely (locked product decision).
func TestTicket_RejectedHasNoStageBar(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	qadmin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}})

	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Bad request"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"x"},
	})
	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")
	eng.mustPost(t, "/tickets/1/pickup", nil)
	eng.mustPost(t, "/tickets/1/transition", url.Values{"action": {"reject"}})

	body := bodyString(t, admin.get("/tickets/1"))
	if strings.Contains(body, "stage-bar") {
		t.Fatal("Rejected ticket should not render the stage progress bar")
	}
}
