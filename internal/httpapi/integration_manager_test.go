package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestQueueSLA_ConfigurableAndAppliedAtTicketCreation covers DESIGN/08 §8.6:
// a Manager can set per-priority SLA minutes on a queue, and new tickets in
// that queue get SLADueAt computed from it (internal/sla.Table), not a
// hardcoded default, once configured.
func TestQueueSLA_ConfigurableAndAppliedAtTicketCreation(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")

	// A bare SystemAdmin must not be able to set SLA targets either (same
	// CapQueueOps gate as the rest of queue ownership).
	resp := admin.postFormNoRedirect("/queues/1/sla", url.Values{
		"minutes_P1": {"30"}, "minutes_P2": {"120"}, "minutes_P3": {"600"}, "minutes_P4": {"2000"},
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("SystemAdmin set SLA: got %d, want 403", resp.StatusCode)
	}

	qadmin.mustPost(t, "/queues/1/sla", url.Values{
		"minutes_P1": {"30"}, "minutes_P2": {"120"}, "minutes_P3": {"600"}, "minutes_P4": {"2000"},
	})

	before := time.Now()
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"P1 outage"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"x"},
	})
	after := time.Now()

	t1 := getTicket(t, env, 1)
	if t1.SLADueAt == nil {
		t.Fatal("expected SLADueAt to be set")
	}
	got := t1.SLADueAt.Sub(before)
	if got < 29*time.Minute || t1.SLADueAt.Sub(after) > 30*time.Minute {
		t.Errorf("SLADueAt = %v after creation, want ~30m (configured P1 target)", got)
	}
}

// TestManagerDashboard_RequiresCapQueueOps covers that the dashboard and
// activity list are Manager-only, and that a Manager's own login lands there
// via the role-based GET / dispatch (DESIGN/08 §8.6).
func TestManagerDashboard_RequiresCapQueueOps(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Router flaky"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"net"},
	})

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")
	resp := eng.get("/manager")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Engineer GET /manager: got %d, want 403", resp.StatusCode)
	}

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	body := bodyString(t, qadmin.get("/manager"))
	if !strings.Contains(body, "Manager dashboard") {
		t.Fatal("expected Manager dashboard page to render")
	}
	if !strings.Contains(body, "1 open") {
		t.Error("expected queue 1's dashboard tile to show 1 open ticket")
	}

	activity := bodyString(t, qadmin.get("/manager/activity"))
	if !strings.Contains(activity, "Router flaky") {
		t.Fatal("expected activity list to show the open ticket")
	}

	// GET / for a Manager dispatches to /manager, not /tickets. client.get's
	// http.Client follows redirects by default, so the final path/body are
	// the dashboard's - only postFormNoRedirect's client stops at the 303.
	resp = qadmin.get("/")
	defer resp.Body.Close()
	if got := resp.Request.URL.Path; got != "/manager" {
		t.Errorf("Manager GET /: final path = %q, want /manager", got)
	}
}

// TestManagerActivity_ShowsLatestNoteAndOrdersByRecency covers the riskiest
// part of the Activity List (DESIGN/08 §8.6): a note alone (no status change)
// must both (a) appear as the row's last-message preview and (b) bump that
// ticket to the top of the recency ordering, via TicketRepo.TouchUpdatedAt.
func TestManagerActivity_ShowsLatestNoteAndOrdersByRecency(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Older ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"x"},
	})
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Newer ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"x"},
	})
	// Touch the older ticket with a note - it should now sort above "Newer ticket".
	admin.mustPost(t, "/tickets/1/notes", url.Values{"body": {"Checking the logs now."}})

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	body := bodyString(t, qadmin.get("/manager/activity"))

	if !strings.Contains(body, "Checking the logs now.") {
		t.Fatal("expected the note body to appear as the last-message preview")
	}
	olderIdx := strings.Index(body, "Older ticket")
	newerIdx := strings.Index(body, "Newer ticket")
	if olderIdx == -1 || newerIdx == -1 || olderIdx > newerIdx {
		t.Errorf("expected 'Older ticket' (freshly noted) to sort above 'Newer ticket', got olderIdx=%d newerIdx=%d", olderIdx, newerIdx)
	}
}

// TestManagerDashboard_MTTxTrendRendersSparklines covers RELEASE/v_3.0.0.md's
// MTTx trend chart: resolving a ticket produces a real, non-flat sparkline on
// the dashboard, not just the current-value summary line.
func TestManagerDashboard_MTTxTrendRendersSparklines(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")

	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Disk full"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"x"},
	})
	admin.mustPost(t, "/tickets/1/pickup", nil)
	admin.mustPost(t, "/tickets/1/transition", url.Values{"action": {"resolve"}})

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	body := bodyString(t, qadmin.get("/manager"))

	if !strings.Contains(body, "last 14 days") {
		t.Error("expected the MTTx trend window label to render")
	}
	if strings.Count(body, "<svg") < 4 {
		t.Errorf("expected 4 sparklines (MTTD/MTTA/MTTM/MTTR), got body: %s", body)
	}
	if !strings.Contains(body, `fill="var(--tw-teal-600)"`) {
		t.Error("expected today's resolution to render a non-empty MTTD sparkline end-marker")
	}
}
