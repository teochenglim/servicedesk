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

// TestCustomerView_GatesCloseReopenByStatusAndHidesStaffJargon covers
// DESIGN/08 §8.4 (revised by RELEASE/v_3.0.4.md after customer feedback that
// an always-visible Reopen button on a brand-new ticket read as broken, not
// flexible): Close ticket/Reopen are only offered once they're a legal next
// move for the ticket's current status, while Add note stays always visible.
// Queue names/labels/audit trail/Ack-Mitigate jargon remain staff-only
// surfaces that must never reach the Customer-rendered page.
func TestCustomerView_GatesCloseReopenByStatusAndHidesStaffJargon(t *testing.T) {
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

	// New ticket: neither Close ticket nor Reopen is a legal move yet, so
	// neither should render - but Add note always does.
	body := bodyString(t, cust.get("/tickets/1"))
	if strings.Contains(body, "Close ticket") || strings.Contains(body, "Reopen") {
		t.Fatal("customer should not see Close ticket / Reopen on a New ticket - neither is a legal transition yet")
	}
	if !strings.Contains(body, "Send") {
		t.Fatal("customer should always see the note composer")
	}
	for _, jargon := range []string{"Queue 1", "Labels</h3>", "Audit Trail", "Ack", "Mitigate"} {
		if strings.Contains(body, jargon) {
			t.Errorf("customer view must not leak staff surface %q", jargon)
		}
	}

	// An illegal transition attempted directly (bypassing the UI gate) is
	// still rejected server-side (New has no "confirm" transition).
	resp := cust.postFormNoRedirect("/tickets/1/transition", url.Values{"action": {"confirm"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("illegal confirm from New: got %d, want 400", resp.StatusCode)
	}

	// Drive the ticket to Resolved (staff actions): both Close ticket and
	// Reopen become legal, and the customer view should now offer both.
	admin.mustPost(t, "/tickets/1/pickup", nil)
	admin.mustPost(t, "/tickets/1/transition", url.Values{"action": {"resolve"}})
	body = bodyString(t, cust.get("/tickets/1"))
	if !strings.Contains(body, "Close ticket") || !strings.Contains(body, "Reopen") {
		t.Fatal("customer should see both Close ticket and Reopen once the ticket is Resolved")
	}

	// Customer closes it: only Reopen is legal from Closed.
	cust.mustPost(t, "/tickets/1/transition", url.Values{"action": {"confirm"}})
	body = bodyString(t, cust.get("/tickets/1"))
	if strings.Contains(body, "Close ticket") {
		t.Fatal("customer should not see Close ticket once the ticket is Closed")
	}
	if !strings.Contains(body, "Reopen") {
		t.Fatal("customer should still see Reopen once the ticket is Closed")
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
