package httpapi

import (
	"net/url"
	"strings"
	"testing"
)

// TestCategory_CRUDIsSystemAdminOnly covers the new Category catalog
// (RELEASE/v_3.0.5.md): SystemAdmin can create/edit/delete rows; Manager
// cannot, same gate as the Service catalog/Custom Field Definitions this
// mirrors. Category #1 ("General") already exists via seedDefaultCategory,
// so this test creates a differently-named row to avoid the uniqueIndex
// collision.
func TestCategory_CRUDIsSystemAdminOnly(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")

	form := url.Values{"name": {"Networking"}, "description_template": {"Please include your device and location."}}
	if resp := qadmin.postForm("/admin/categories", form); resp.StatusCode != 403 {
		t.Fatalf("Manager POST /admin/categories = %d, want 403", resp.StatusCode)
	}
	if resp := qadmin.get("/admin/categories"); resp.StatusCode != 403 {
		t.Fatalf("Manager GET /admin/categories = %d, want 403", resp.StatusCode)
	}

	admin.mustPost(t, "/admin/categories", form)
	body := bodyString(t, admin.get("/admin/categories"))
	if !strings.Contains(body, `Networking <span class="muted">(#`) {
		t.Fatal("SystemAdmin should be able to create a category and see it listed")
	}

	admin.mustPost(t, "/admin/categories/2", url.Values{
		"name": {"Networking"}, "title_template": {"[Network] "},
	})
	body = bodyString(t, admin.get("/admin/categories"))
	if !strings.Contains(body, "[Network] ") {
		t.Fatal("SystemAdmin should be able to update a category's title template")
	}

	if resp := qadmin.postForm("/admin/categories/2/delete", nil); resp.StatusCode != 403 {
		t.Fatalf("Manager delete = %d, want 403", resp.StatusCode)
	}
	admin.mustPost(t, "/admin/categories/2/delete", nil)
	body = bodyString(t, admin.get("/admin/categories"))
	if strings.Contains(body, `Networking <span class="muted">(#`) {
		t.Fatal("SystemAdmin delete should have removed the category")
	}
}

// TestTicketNew_CustomerFormHasNoQueueFieldAndDefaultsToQueueOne covers
// RELEASE/v_3.0.5.md: a Customer never picks a Queue at submission - the
// field isn't even rendered, and a ticket created without one lands in the
// guaranteed default Queue #1. Staff still see and can pick a Queue on the
// same form (raising a ticket on behalf of someone, DESIGN/08 §8.5).
func TestTicketNew_CustomerFormHasNoQueueFieldAndDefaultsToQueueOne(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createOrg(t, admin, "Acme Corp")
	createUser(t, admin, "cust1", "c1@x.com", "pass123", "Customer")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"3"}})

	cust := env.client()
	cust.mustLogin("Acme Corp", "cust1", "pass123")

	formBody := bodyString(t, cust.get("/tickets/new"))
	if strings.Contains(formBody, `name="queue_id"`) {
		t.Fatal("customer's ticket-new form must not have a Queue field")
	}
	if !strings.Contains(formBody, `name="category"`) {
		t.Fatal("customer's ticket-new form should have a Category field")
	}

	staffBody := bodyString(t, admin.get("/tickets/new"))
	if !strings.Contains(staffBody, `name="queue_id"`) {
		t.Fatal("staff's ticket-new form should still have a Queue field")
	}

	// No queue_id submitted at all - must default to Queue #1, not error.
	cust.mustPost(t, "/tickets", url.Values{
		"title": {"Cannot access email"}, "description": {"d"}, "category": {"General"},
	})
	// Customer's own view never renders "Queue N" (staff-only jargon), so
	// check via a staff view that the ticket actually landed in Queue #1.
	staffTicketBody := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(staffTicketBody, "Queue 1") {
		t.Fatal("ticket created without queue_id should default to Queue #1")
	}
}
