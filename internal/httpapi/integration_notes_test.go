package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestNotes_InternalHiddenFromCustomer(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "cust1", "c1@x.com", "pass123", "Customer")
	createOrg(t, admin, "Acme Corp")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"3"}}) // cust1

	cust := env.client()
	cust.mustLogin("Acme Corp", "cust1", "pass123")
	cust.mustPost(t, "/tickets", url.Values{
		"title": {"Login issue"}, "description": {"can't log in"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"access"},
	})

	admin.mustPost(t, "/tickets/1/notes", url.Values{"body": {"internal-only debug info"}, "internal": {"on"}})
	admin.mustPost(t, "/tickets/1/notes", url.Values{"body": {"we are looking into it"}})

	custView := bodyString(t, cust.get("/tickets/1"))
	if strings.Contains(custView, "internal-only debug info") {
		t.Fatal("customer must not see internal notes")
	}
	if !strings.Contains(custView, "we are looking into it") {
		t.Fatal("customer should see external notes")
	}

	adminView := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(adminView, "internal-only debug info") {
		t.Fatal("staff should see internal notes")
	}
}

// TestCustomerView_AlwaysShowsCloseReopenAndHidesStaffJargon covers DESIGN/08
// §8.4: Close/Reopen must be offered to a Customer regardless of ticket
// status (the state machine still legitimately rejects an illegal one
// server-side), and queue names/labels/audit trail/Ack-Mitigate jargon are
// staff-only surfaces that must never reach the Customer-rendered page.
func TestCustomerView_AlwaysShowsCloseReopenAndHidesStaffJargon(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createOrg(t, admin, "Acme Corp")
	createUser(t, admin, "cust1", "c1@x.com", "pass123", "Customer")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"3"}}) // cust1

	cust := env.client()
	cust.mustLogin("Acme Corp", "cust1", "pass123")
	cust.mustPost(t, "/tickets", url.Values{
		"title": {"Login issue"}, "description": {"can't log in"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"access"},
	})
	admin.mustPost(t, "/tickets/1/labels", url.Values{"name": {"database"}, "kind": {"incident"}})

	// New ticket: Close/Reopen must still be offered, even though the state
	// machine doesn't actually allow either action from New yet.
	body := bodyString(t, cust.get("/tickets/1"))
	if !strings.Contains(body, "Close ticket") || !strings.Contains(body, "Reopen") {
		t.Fatal("customer should always see Close ticket / Reopen, even on a New ticket")
	}
	for _, jargon := range []string{"Queue 1", "Labels</h3>", "Audit Trail", "Ack", "Mitigate"} {
		if strings.Contains(body, jargon) {
			t.Errorf("customer view must not leak staff surface %q", jargon)
		}
	}

	// An illegal transition attempted via the always-visible button is still
	// rejected server-side (New has no "confirm" transition).
	resp := cust.postFormNoRedirect("/tickets/1/transition", url.Values{"action": {"confirm"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("illegal confirm from New: got %d, want 400", resp.StatusCode)
	}

	// Staff still sees queue/labels/audit trail and the technical stage wording.
	staffBody := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(staffBody, "Labels</h3>") || !strings.Contains(staffBody, "Audit Trail") {
		t.Fatal("staff view should still show Labels/Audit Trail")
	}
}

func TestLabels_AddAndRemove(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Server down"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"infra"},
	})
	admin.mustPost(t, "/tickets/1/labels", url.Values{"name": {"database"}, "kind": {"incident"}})

	body := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "database") {
		t.Fatal("expected label 'database' on ticket page")
	}
}

func TestWatch_GrantsCrossUserVisibilityWithinSameOrg(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	createOrg(t, admin, "Acme Corp")
	createUser(t, admin, "alice", "a@acme.com", "pass123", "Customer")
	createUser(t, admin, "bob", "b@acme.com", "pass123", "Customer")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"3"}}) // alice
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"4"}}) // bob

	alice := env.client()
	alice.mustLogin("Acme Corp", "alice", "pass123")
	alice.mustPost(t, "/tickets", url.Values{
		"title": {"Alice's VPN issue"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"net"},
	})

	bob := env.client()
	bob.mustLogin("Acme Corp", "bob", "pass123")

	// Before watching, bob (same org, not creator) cannot see alice's ticket.
	resp := bob.get("/tickets/1")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bob should not see alice's ticket yet, got %d", resp.StatusCode)
	}

	bob.postForm("/tickets/1/watch", nil).Body.Close()

	resp = bob.get("/tickets/1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("bob should see the ticket after watching it, got %d", resp.StatusCode)
	}
}
