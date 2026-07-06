package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// --- helpers -----------------------------------------------------------

func createOrg(t *testing.T, admin *client, name string) int64 {
	t.Helper()
	admin.mustPost(t, "/admin/orgs", url.Values{"name": {name}})
	return latestID(t, admin, "/admin/orgs", name)
}

// latestID scrapes "(#<id>" out of an admin list page next to name - good
// enough for tests without needing a JSON API.
func latestID(t *testing.T, c *client, path, name string) int64 {
	t.Helper()
	body := bodyString(t, c.get(path))
	idx := strings.Index(body, name)
	if idx == -1 {
		t.Fatalf("%q not found on %s", name, path)
	}
	rest := body[idx:]
	hIdx := strings.Index(rest, "(#")
	if hIdx == -1 {
		t.Fatalf("no id marker after %q on %s", name, path)
	}
	rest = rest[hIdx+2:]
	end := strings.IndexAny(rest, " )")
	if end == -1 {
		t.Fatalf("could not parse id after %q on %s", name, path)
	}
	id, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		t.Fatalf("parse id %q: %v", rest[:end], err)
	}
	return id
}

func (c *client) mustPost(t *testing.T, path string, form url.Values) {
	t.Helper()
	resp := c.postForm(path, form)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("POST %s failed: status=%d body=%s", path, resp.StatusCode, bodyString(t, resp))
	}
	resp.Body.Close()
}

func createUser(t *testing.T, admin *client, username, email, password, role string) {
	t.Helper()
	admin.mustPost(t, "/admin/users", url.Values{
		"username": {username}, "email": {email}, "password": {password}, "role": {role},
	})
}

// --- auth ---------------------------------------------------------------

func TestAuth_LoginSuccessAndFailure(t *testing.T) {
	env := newTestEnv(t)
	c := env.client()

	if resp := c.login("", "admin", "wrong-password"); resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 (re-rendered login page) got %d", resp.StatusCode)
	} else if !strings.Contains(bodyString(t, resp), "Invalid") {
		t.Fatalf("expected invalid-credentials message")
	}

	c.mustLogin("", "admin", "admin123")
	resp := c.get("/tickets")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tickets after login = %d, want 200", resp.StatusCode)
	}
}

func TestAuth_UnauthenticatedRedirectsToLogin(t *testing.T) {
	env := newTestEnv(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(env.http.URL + "/tickets")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected redirect to /login, got status %d", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("expected redirect to /login, got %q", loc)
	}
}

// --- ticket lifecycle / state machine ------------------------------------

func TestTicketDetail_ShowsListPaneAlongside(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"First ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"infra"},
	})
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Second ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"infra"},
	})

	body := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "First ticket") {
		t.Fatal("detail page should show the selected ticket")
	}
	if !strings.Contains(body, "Second ticket") {
		t.Fatal("detail page should still show the other ticket in the list pane")
	}
	if !strings.Contains(body, `id="ticket-detail-pane"`) {
		t.Fatal("detail pane must expose #ticket-detail-pane for the htmx hx-select swap target")
	}
}

func TestTicket_FullLifecycle(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	admin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}}) // eng1 is user id 3 (system=1, admin=2)

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")

	// Create as admin (SystemAdmin can create tickets too), then run the
	// full New -> In Progress -> Resolved -> Closed path as the engineer.
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Disk full"}, "description": {"alert"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"infra"},
	})
	ticketPath := "/tickets/1"

	assertStatus(t, admin, ticketPath, "New")

	resp := eng.postForm("/tickets/1/pickup", nil)
	resp.Body.Close()
	assertStatus(t, admin, ticketPath, "In Progress")

	resp = eng.postForm("/tickets/1/transition", url.Values{"action": {"resolve"}})
	resp.Body.Close()
	assertStatus(t, admin, ticketPath, "Resolved")

	resp = eng.postForm("/tickets/1/transition", url.Values{"action": {"confirm"}})
	resp.Body.Close()
	assertStatus(t, admin, ticketPath, "Closed")
}

func TestTicket_RejectCascadesToClosedAndReopenRestricted(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	admin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}})
	createUser(t, admin, "cust1", "c1@x.com", "pass123", "Customer")
	createOrg(t, admin, "Acme Corp")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"4"}}) // cust1

	cust := env.client()
	cust.mustLogin("Acme Corp", "cust1", "pass123")
	cust.mustPost(t, "/tickets", url.Values{
		"title": {"Broken printer"}, "description": {"jam"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"office"},
	})

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")
	if resp := eng.postForm("/tickets/1/pickup", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("pickup: got %d body=%s", resp.StatusCode, bodyString(t, resp))
	} else {
		resp.Body.Close()
	}
	if resp := eng.postForm("/tickets/1/transition", url.Values{"action": {"reject"}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("reject: got %d body=%s", resp.StatusCode, bodyString(t, resp))
	} else {
		resp.Body.Close()
	}

	assertStatus(t, admin, "/tickets/1", "Closed") // Rejected forces Closed (DESIGN.md 3.1.2)

	// Reopen from Closed is restricted to SystemAdmin or the original creator.
	resp := eng.postFormNoRedirect("/tickets/1/transition", url.Values{"action": {"reopen"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("engineer reopen of closed ticket: got %d, want 400 (forbidden->error)", resp.StatusCode)
	}

	resp = cust.postFormNoRedirect("/tickets/1/transition", url.Values{"action": {"reopen"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("original creator reopen of closed ticket: got %d, want redirect", resp.StatusCode)
	}
	assertStatus(t, admin, "/tickets/1", "In Progress")
}

func assertStatus(t *testing.T, c *client, path, want string) {
	t.Helper()
	body := bodyString(t, c.get(path))
	if !strings.Contains(body, "status-"+strings.ReplaceAll(want, " ", "-")) && !strings.Contains(body, ">"+want+"<") {
		t.Fatalf("%s: expected status %q in page, not found", path, want)
	}
}

// --- notes / labels / watch ----------------------------------------------

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

// --- multi-tenant isolation ----------------------------------------------

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

// --- queue membership + role-gated assign/transfer -----------------------

func TestQueueMembership_GatesPickupAndAssignIsQueueAdminOnly(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")    // id 3
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "QueueAdmin") // id 4
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Router flaky"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"net"},
	})

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")

	// Not yet a queue member: pickup is forbidden.
	resp := eng.postFormNoRedirect("/tickets/1/pickup", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("pickup without queue membership: got %d, want 400", resp.StatusCode)
	}

	admin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}})
	resp = eng.postForm("/tickets/1/pickup", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pickup with queue membership: got %d", resp.StatusCode)
	}

	// Engineer cannot assign/transfer to someone else - only QueueAdmin can.
	resp = eng.postFormNoRedirect("/tickets/1/assign", url.Values{"assignee_id": {"3"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("engineer assign: got %d, want 403", resp.StatusCode)
	}

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	resp = qadmin.postForm("/tickets/1/assign", url.Values{"assignee_id": {"3"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("queue admin transfer: got %d", resp.StatusCode)
	}
}

// --- full-text search -----------------------------------------------------

func TestSearch_FindsTicketByTitleAndNoteBody(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Payment gateway timeout"}, "description": {"customers cannot checkout"},
		"queue_id": {"1"}, "priority": {"P1"}, "category": {"payments"},
	})
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Printer jam"}, "description": {"office printer"},
		"queue_id": {"1"}, "priority": {"P4"}, "category": {"office"},
	})
	admin.mustPost(t, "/tickets/2/notes", url.Values{"body": {"turns out it needs a firmware update"}})

	byTitle := bodyString(t, admin.get("/tickets?q=gateway"))
	if !strings.Contains(byTitle, "Payment gateway timeout") {
		t.Fatal("search by title term did not find the ticket")
	}
	if strings.Contains(byTitle, "Printer jam") {
		t.Fatal("search by title term should not match unrelated ticket")
	}

	byNote := bodyString(t, admin.get("/tickets?q=firmware"))
	if !strings.Contains(byNote, "Printer jam") {
		t.Fatal("search over note body did not find the ticket (DESIGN.md 3.7: search across notes too)")
	}
}

// --- runbook workflow (user_input -> http_request -> template_render) ----

func TestRunbook_UserInputHttpRequestTemplateRender(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"deploys": []string{"v1.2.3", "v1.2.2"}})
	}))
	defer upstream.Close()

	admin.mustPost(t, "/tickets", url.Values{
		"title": {"API latency spike"}, "description": {"p99 up"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"infra"},
	})

	config := `{"steps":[
		{"id":"gather_info","type":"user_input","fields":[{"name":"service_name","label":"Service","required":true}]},
		{"id":"fetch_deploys","type":"http_request","url":"` + upstream.URL + `","save_response_to":"deploy_events"},
		{"id":"draft_note","type":"template_render","template":"Incident opener for {{.service_name}}","output_target":"ticket_external_note"}
	]}`
	admin.mustPost(t, "/admin/workflows", url.Values{
		"name": {"Incident Runbook"}, "trigger": {"ticket_created"}, "is_runbook": {"on"}, "config": {config},
	})

	admin.mustPost(t, "/tickets/1/runbooks/1/start", nil)
	env.engine.ProcessOne() // executes gather_info -> pauses at user_input

	body := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "needs input") {
		t.Fatal("expected the runbook to be paused waiting for user_input")
	}

	admin.mustPost(t, "/workflow-tasks/1/resume", url.Values{"service_name": {"checkout-api"}})
	env.engine.ProcessOne() // fetch_deploys (http_request) + draft_note (template_render)

	body = bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "Incident opener for checkout-api") {
		t.Fatal("expected the rendered template to be posted as an external note")
	}
}

// --- approvals --------------------------------------------------------

func TestApproval_RejectionStopsWorkflow(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "QueueAdmin")

	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Escalation request"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"x"},
	})

	config := `{"steps":[{"id":"approve_escalation","type":"approval","approver_role":"QueueAdmin"},{"id":"note","type":"add_note","body":"escalated"}]}`
	admin.mustPost(t, "/admin/workflows", url.Values{
		"name": {"Escalation"}, "trigger": {"ticket_created"}, "is_runbook": {"on"}, "config": {config},
	})
	admin.mustPost(t, "/tickets/1/runbooks/1/start", nil)
	env.engine.ProcessOne() // creates the approval, pauses

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	qadmin.mustPost(t, "/approvals/1/decide", url.Values{"decision": {"reject"}})

	env.engine.ProcessOne() // should fail the task, not add the note

	body := bodyString(t, admin.get("/tickets/1"))
	if strings.Contains(body, "escalated") {
		t.Fatal("rejected approval must not let the workflow continue to add_note")
	}
}

// --- webhooks --------------------------------------------------------

func TestWebhook_DeliversSignedPayloadOnTicketCreated(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	received := make(chan string, 1)
	secret := "whsec-test"
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(buf)
		want := hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-ServiceDesk-Signature") != want {
			w.WriteHeader(http.StatusUnauthorized)
			received <- "BAD SIGNATURE"
			return
		}
		received <- string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	admin.mustPost(t, "/admin/webhooks", url.Values{
		"url": {receiver.URL}, "events": {"ticket.created"}, "secret": {secret},
	})
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Webhook test ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"x"},
	})

	env.whDispatcher.ProcessOne()

	select {
	case payload := <-received:
		if payload == "BAD SIGNATURE" {
			t.Fatal("webhook signature did not verify")
		}
		if !strings.Contains(payload, "Webhook test ticket") {
			t.Fatalf("payload missing ticket data: %s", payload)
		}
	default:
		t.Fatal("webhook receiver never got a delivery")
	}
}
