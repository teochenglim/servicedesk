package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"servicedesk/internal/llm"
)

// TestAI_RoutesDisabledByDefault covers the off-by-default safety rail:
// newTestEnv (no AI client) must not register any AI routes at all. The path
// shape "/tickets/draft-description" collides with the wildcard
// "GET /tickets/{id}" pattern, so Go's mux reports 405 (method exists at that
// path shape, just not for POST) rather than 404 - either way, the point is
// no AI handler ever runs.
func TestAI_RoutesDisabledByDefault(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	resp := admin.postForm("/tickets/draft-description", url.Values{"rough": {"x"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("draft-description with AI disabled: got %d, want 405", resp.StatusCode)
	}
}

func TestAI_DraftDescriptionReturnsJSON(t *testing.T) {
	fake := &llm.FakeClient{Response: "Checkout fails intermittently with a 504, started this morning."}
	env := newTestEnvWithAI(t, fake)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	resp := admin.postForm("/tickets/draft-description", url.Values{"rough": {"checkout broken sometimes"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("draft-description: got %d", resp.StatusCode)
	}
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if out["draft"] != fake.Response {
		t.Errorf("draft = %q, want %q", out["draft"], fake.Response)
	}
}

// TestAI_DraftResolutionIsEngineerOnly covers the route gate: description
// drafting is open to any authenticated user (Customer included), but
// resolution/transfer drafting on an existing ticket is Engineer-facing only.
func TestAI_DraftResolutionIsEngineerOnly(t *testing.T) {
	fake := &llm.FakeClient{Response: "Resolved by renewing the cert."}
	env := newTestEnvWithAI(t, fake)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createOrg(t, admin, "Acme Corp")
	createUser(t, admin, "cust1", "c1@x.com", "pass123", "Customer")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"3"}})
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"VPN drops"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"net"},
	})

	cust := env.client()
	cust.mustLogin("Acme Corp", "cust1", "pass123")
	resp := cust.postFormNoRedirect("/tickets/1/ai-draft", url.Values{"kind": {"resolution"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("customer ai-draft resolution: got %d, want 403", resp.StatusCode)
	}

	resp = admin.postForm("/tickets/1/ai-draft", url.Values{"kind": {"resolution"}})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("staff ai-draft resolution: got %d", resp.StatusCode)
	}
}

// TestAI_SummaryPanel_RegeneratesOnNoteAndLocksEditedField exercises the
// full loop: posting a note asynchronously regenerates the panel, the panel
// is staff-only, and a human edit survives a subsequent regeneration
// (DESIGN/08 §8.9's core human-edit-outranks-regeneration rule).
func TestAI_SummaryPanel_RegeneratesOnNoteAndLocksEditedField(t *testing.T) {
	fake := &llm.FakeClient{Response: `{"symptom":"VPN drops","what_tried":"","problem_statement":"","diagnosis":"","mitigation":"","resolution":""}`}
	env := newTestEnvWithAI(t, fake)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createOrg(t, admin, "Acme Corp")
	createUser(t, admin, "cust1", "c1@x.com", "pass123", "Customer")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"3"}})
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"VPN drops"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"net"},
	})
	admin.mustPost(t, "/tickets/1/notes", url.Values{"body": {"Checking the logs now."}})

	if !pollUntil(t, 2*time.Second, func() bool {
		return strings.Contains(bodyString(t, admin.get("/tickets/1")), "VPN drops</p>")
	}) {
		t.Fatal("timed out waiting for the async AI summary regeneration to land")
	}

	// The panel is Engineer-facing only - a Customer must never see it.
	cust := env.client()
	cust.mustLogin("Acme Corp", "cust1", "pass123")
	custBody := bodyString(t, cust.get("/tickets/1"))
	if strings.Contains(custBody, "AI summary") {
		t.Fatal("Customer must not see the AI Ticket Intelligence Panel")
	}

	// Human edit locks the field.
	admin.mustPost(t, "/tickets/1/ai-summary/symptom/edit", url.Values{"value": {"human-corrected symptom"}})
	body := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "human-corrected symptom") {
		t.Fatal("expected the edited field value to render")
	}

	// A further regeneration must not clobber the locked field.
	fake.Response = `{"symptom":"AI overwrite attempt","what_tried":"","problem_statement":"","diagnosis":"","mitigation":"","resolution":""}`
	admin.mustPost(t, "/tickets/1/ai-summary/regenerate", nil)
	body = bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "human-corrected symptom") {
		t.Fatal("locked field must survive a manual regenerate-all")
	}
	if strings.Contains(body, "AI overwrite attempt") {
		t.Fatal("locked field must not be overwritten by regeneration")
	}
}

// pollUntil polls cond every 20ms until it returns true or timeout elapses.
func pollUntil(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}
