package httpapi

import (
	"net/url"
	"strings"
	"testing"
)

// setupResolvedTicket creates an Engineer + Customer, files a ticket, and
// resolves it - which fires the Knowledge Base propose-on-resolve trigger
// (RELEASE/v_2.1.0.md) and leaves exactly one draft KBArticle behind.
func setupResolvedTicket(t *testing.T, env *testEnv) (admin, eng, cust *client) {
	t.Helper()
	admin = env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	createUser(t, admin, "cust1", "c1@x.com", "pass123", "Customer")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	createOrg(t, admin, "Acme Corp")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"4"}}) // cust1
	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	qadmin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}}) // eng1

	cust = env.client()
	cust.mustLogin("Acme Corp", "cust1", "pass123")
	cust.mustPost(t, "/tickets", url.Values{
		"title": {"Vendor emails going to spam"}, "description": {"Legit vendor emails land in junk"},
		"queue_id": {"1"}, "priority": {"P3"}, "category": {"email"},
	})

	eng = env.client()
	eng.mustLogin("", "eng1", "pass123")
	eng.mustPost(t, "/tickets/1/pickup", nil)
	eng.mustPost(t, "/tickets/1/transition", url.Values{"action": {"resolve"}})
	return admin, eng, cust
}

// TestKB_UnapprovedDraftNeverReachesCustomer is the one true trust-boundary
// test called out in RELEASE/v_2.1.0.md's verification checklist: a draft
// must 404 at /kb/{id} and be absent from /kb for a Customer, and only
// becomes visible after an Engineer approves it.
func TestKB_UnapprovedDraftNeverReachesCustomer(t *testing.T) {
	env := newTestEnv(t)
	_, eng, cust := setupResolvedTicket(t, env)

	if resp := cust.get("/kb/1"); resp.StatusCode != 404 {
		t.Fatalf("customer GET /kb/1 (draft) = %d, want 404", resp.StatusCode)
	}
	if strings.Contains(bodyString(t, cust.get("/kb")), "Vendor emails going to spam") {
		t.Fatal("draft article must not appear in the customer-facing /kb list")
	}

	// Engineer can see the draft in the curation queue and at /kb/{id}.
	if resp := eng.get("/kb/1"); resp.StatusCode != 200 {
		t.Fatalf("engineer GET /kb/1 (draft) = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(bodyString(t, eng.get("/kb/review")), "Vendor emails going to spam") {
		t.Fatal("draft should be listed in the Engineer curation queue")
	}

	eng.mustPost(t, "/kb/1/approve", nil)

	if resp := cust.get("/kb/1"); resp.StatusCode != 200 {
		t.Fatalf("customer GET /kb/1 (published) = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(bodyString(t, cust.get("/kb")), "Vendor emails going to spam") {
		t.Fatal("published article should now appear in the customer-facing /kb list")
	}
}

// TestKB_ResolveProposesDraftExactlyOncePerResolution covers the other
// checklist item: ticket resolve -> propose fires exactly once per
// resolution, including again after a reopen -> re-resolve cycle (not a
// silent no-op, and not a duplicate on the same resolution).
func TestKB_ResolveProposesDraftExactlyOncePerResolution(t *testing.T) {
	env := newTestEnv(t)
	_, eng, _ := setupResolvedTicket(t, env)

	drafts := bodyString(t, eng.get("/kb/review"))
	if strings.Count(drafts, "Vendor emails going to spam") != 1 {
		t.Fatalf("expected exactly 1 draft after first resolution, review queue: %s", drafts)
	}

	eng.mustPost(t, "/tickets/1/transition", url.Values{"action": {"reopen"}})
	eng.mustPost(t, "/tickets/1/transition", url.Values{"action": {"resolve"}})

	drafts = bodyString(t, eng.get("/kb/review"))
	if strings.Count(drafts, "Vendor emails going to spam") != 2 {
		t.Fatalf("expected exactly 2 independent drafts after reopen -> re-resolve, review queue: %s", drafts)
	}
}
