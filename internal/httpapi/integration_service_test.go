package httpapi

import (
	"net/url"
	"strings"
	"testing"
)

// TestServiceCatalog_CRUDIsSystemAdminOnly covers the "editable field, must
// be CRUD using admin role" requirement (RELEASE/v_2.1.0.md): SystemAdmin can
// create/edit/delete Service catalog rows; Manager/Engineer cannot, even
// though Manager holds CapQueueOps (this is gated like Users/Webhooks/
// CustomFieldDef, not like Queue's day-to-day routing/SLA config).
func TestServiceCatalog_CRUDIsSystemAdminOnly(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")

	form := url.Values{"name": {"Mail Service"}, "criticality": {"Critical"}, "status": {"active"}}
	for _, c := range []*client{qadmin, eng} {
		if resp := c.postForm("/admin/services", form); resp.StatusCode != 403 {
			t.Fatalf("non-SystemAdmin POST /admin/services = %d, want 403", resp.StatusCode)
		}
		if resp := c.get("/admin/services"); resp.StatusCode != 403 {
			t.Fatalf("non-SystemAdmin GET /admin/services = %d, want 403", resp.StatusCode)
		}
	}

	admin.mustPost(t, "/admin/services", form)
	// Note: "Mail Service" also appears as static placeholder text in the
	// "New service" form, so assert on the row heading specifically, not a
	// bare substring match.
	body := bodyString(t, admin.get("/admin/services"))
	if !strings.Contains(body, `Mail Service <span class="muted">(#`) {
		t.Fatal("SystemAdmin should be able to create a service and see it listed")
	}

	admin.mustPost(t, "/admin/services/1", url.Values{
		"name": {"Mail Service"}, "criticality": {"High"}, "status": {"active"},
	})
	body = bodyString(t, admin.get("/admin/services"))
	if !strings.Contains(body, "High") {
		t.Fatal("SystemAdmin should be able to update a service's criticality")
	}

	if resp := eng.postForm("/admin/services/1/delete", nil); resp.StatusCode != 403 {
		t.Fatalf("non-SystemAdmin delete = %d, want 403", resp.StatusCode)
	}
	admin.mustPost(t, "/admin/services/1/delete", nil)
	body = bodyString(t, admin.get("/admin/services"))
	if strings.Contains(body, `Mail Service <span class="muted">(#`) {
		t.Fatal("SystemAdmin delete should have removed the service")
	}
}

// TestCustomFields_CRUDIsSystemAdminOnly covers moving CustomFieldDef CRUD
// off Manager's CapQueueOps gate onto SystemAdmin (RELEASE/v_3.0.0.md) -
// defining what data gets collected is a system-configuration concern, same
// bucket as the Service catalog, not Queue's day-to-day routing/SLA config.
func TestCustomFields_CRUDIsSystemAdminOnly(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")

	form := url.Values{"category": {"network"}, "name": {"vlan"}, "label": {"VLAN"}, "type": {"text"}}
	if resp := qadmin.postForm("/admin/custom-fields", form); resp.StatusCode != 403 {
		t.Fatalf("Manager POST /admin/custom-fields = %d, want 403 (even though Manager holds CapQueueOps)", resp.StatusCode)
	}
	if resp := qadmin.get("/admin/custom-fields"); resp.StatusCode != 403 {
		t.Fatalf("Manager GET /admin/custom-fields = %d, want 403", resp.StatusCode)
	}

	admin.mustPost(t, "/admin/custom-fields", form)
	body := bodyString(t, admin.get("/admin/custom-fields"))
	if !strings.Contains(body, "vlan") {
		t.Fatal("SystemAdmin should be able to create a custom field definition")
	}
}

// TestServiceCatalog_TicketLinkAllowsUnknown covers "ticket level, but allow
// unknown": a ticket's impacted service is optional at creation and can be
// set/cleared later by an Engineer at triage.
func TestServiceCatalog_TicketLinkAllowsUnknown(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	admin.mustPost(t, "/admin/services", url.Values{"name": {"Mail Service"}, "criticality": {"Critical"}, "status": {"active"}})

	// No service_id at all - "unknown" must be a valid, non-erroring default.
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Cannot access email"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"email"},
	})

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")
	eng.mustPost(t, "/tickets/1/service", url.Values{"service_id": {"1"}})
	body := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, `selected>Mail Service`) {
		t.Fatal("triage should be able to set the impacted service")
	}

	// Clearing it back to "unknown" (empty selection) must also work.
	eng.mustPost(t, "/tickets/1/service", url.Values{"service_id": {""}})
}
