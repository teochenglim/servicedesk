package httpapi

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"servicedesk/internal/llm"
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

// TestKB_MatchSymptomEndpoint covers the submission-time suggestion popup's
// backend (RELEASE/v_3.0.0.md): a published article with real Symptom/
// WhatToObserve text matches similar wording and misses unrelated wording.
func TestKB_MatchSymptomEndpoint(t *testing.T) {
	env := newTestEnv(t)
	_, eng, _ := setupResolvedTicket(t, env)
	eng.mustPost(t, "/kb/1", url.Values{
		"title": {"VPN drops"}, "symptom": {"vpn connection drops every ten minutes"},
		"what_to_observe": {"session disconnects repeatedly"},
	})
	eng.mustPost(t, "/kb/1/approve", nil)

	resp := eng.postForm("/tickets/match-symptom", url.Values{
		"title": {""}, "description": {"vpn connection drops every few minutes"},
	})
	var out struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		Resolution string `json:"resolution"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if out.ID != 1 || out.Title != "VPN drops" {
		t.Fatalf("expected a match on article 1, got %+v", out)
	}

	resp = eng.postForm("/tickets/match-symptom", url.Values{
		"title": {""}, "description": {"completely unrelated printer jam issue"},
	})
	out = struct {
		ID         int64  `json:"id"`
		Title      string `json:"title"`
		Resolution string `json:"resolution"`
	}{}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	resp.Body.Close()
	if out.ID != 0 {
		t.Fatalf("expected no match for unrelated text, got %+v", out)
	}
}

// TestKB_TriageSuggestionShowsOnDetailPage covers the triage-time half:
// once the AI panel produces a "symptom" matching a published article, the
// ticket detail page surfaces it to staff.
func TestKB_TriageSuggestionShowsOnDetailPage(t *testing.T) {
	fake := &llm.FakeClient{Response: `{"symptom":"vpn connection drops every few minutes","what_tried":"","problem_statement":"","diagnosis":"","mitigation":"","resolution":""}`}
	env := newTestEnvWithAI(t, fake)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	// First ticket becomes the published article a real Symptom comes from.
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"VPN drops"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"net"},
	})
	admin.mustPost(t, "/tickets/1/pickup", nil)
	admin.mustPost(t, "/tickets/1/transition", url.Values{"action": {"resolve"}})
	admin.mustPost(t, "/kb/1", url.Values{
		"title": {"VPN drops"}, "symptom": {"vpn connection drops every ten minutes"},
		"what_to_observe": {"session disconnects repeatedly"},
	})
	admin.mustPost(t, "/kb/1/approve", nil)

	// Second ticket: its AI panel will extract a similar symptom once a note lands.
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"VPN unstable"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"net"},
	})
	admin.mustPost(t, "/tickets/2/notes", url.Values{"body": {"investigating"}})

	if !pollUntil(t, 2*time.Second, func() bool {
		return strings.Contains(bodyString(t, admin.get("/tickets/2")), "Similar past tickets")
	}) {
		t.Fatal("timed out waiting for the triage-time KB suggestion to appear")
	}
}
