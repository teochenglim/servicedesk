package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestMultiTenant_CrossOrgIsolationAndWrongOrgLoginRejected(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	createOrg(t, admin, "Acme Corp")
	createOrg(t, admin, "Globex Inc")
	createUser(t, admin, "alice", "a@acme.com", "pass123", "Customer")
	createUser(t, admin, "carol", "c@globex.com", "pass123", "Customer")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"3"}}) // alice -> Acme
	admin.mustPost(t, "/admin/orgs/2/members", url.Values{"user_id": {"4"}}) // carol -> Globex

	// alice cannot log into the wrong org even with correct credentials.
	wrongOrg := env.client()
	resp := wrongOrg.login("Globex Inc", "alice", "pass123")
	if body := bodyString(t, resp); !strings.Contains(body, "Invalid organization") {
		t.Fatalf("expected org-mismatch rejection, got: %s", body)
	}

	alice := env.client()
	alice.mustLogin("Acme Corp", "alice", "pass123")
	alice.mustPost(t, "/tickets", url.Values{
		"title": {"Acme ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"x"},
	})

	carol := env.client()
	carol.mustLogin("Globex Inc", "carol", "pass123")
	carol.mustPost(t, "/tickets", url.Values{
		"title": {"Globex ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"x"},
	})

	resp = carol.get("/tickets/1") // alice's (Acme) ticket
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("carol (Globex) must not see Acme's ticket, got %d", resp.StatusCode)
	}

	aliceList := bodyString(t, alice.get("/tickets"))
	if strings.Contains(aliceList, "Globex ticket") {
		t.Fatal("alice's ticket list leaked Globex's ticket")
	}
	if !strings.Contains(aliceList, "Acme ticket") {
		t.Fatal("alice's ticket list is missing her own ticket")
	}
}

func TestQueueMembership_GatesPickupAndAssignIsManagerOnly(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer") // id 3
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager") // id 4
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Router flaky"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"net"},
	})

	// Queue CRUD/membership requires CapQueueOps (Manager only) - SystemAdmin
	// no longer holds it natively (DESIGN/02 §2.1.1), so a bare admin request
	// must 403 here, and queue membership setup below goes through qadmin.
	resp := admin.postFormNoRedirect("/queues/1/members", url.Values{"user_id": {"3"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("SystemAdmin queue-member add without sudo: got %d, want 403", resp.StatusCode)
	}

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")

	// Not yet a queue member: pickup is forbidden.
	resp = eng.postFormNoRedirect("/tickets/1/pickup", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pickup without queue membership: got %d, want 400", resp.StatusCode)
	}

	qadmin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}})
	resp = eng.postForm("/tickets/1/pickup", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pickup with queue membership: got %d", resp.StatusCode)
	}

	// Engineer cannot assign/transfer to someone else - only Manager can.
	resp = eng.postFormNoRedirect("/tickets/1/assign", url.Values{"assignee_id": {"3"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("engineer assign: got %d, want 403", resp.StatusCode)
	}

	resp = qadmin.postForm("/tickets/1/assign", url.Values{"assignee_id": {"3"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manager transfer: got %d", resp.StatusCode)
	}
}
