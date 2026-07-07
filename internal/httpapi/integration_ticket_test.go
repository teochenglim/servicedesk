package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

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
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	qadmin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}}) // eng1 is user id 3 (system=1, admin=2)

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
	createUser(t, admin, "cust1", "c1@x.com", "pass123", "Customer")
	createOrg(t, admin, "Acme Corp")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"4"}}) // cust1
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	qadmin.mustPost(t, "/queues/1/members", url.Values{"user_id": {"3"}})

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
