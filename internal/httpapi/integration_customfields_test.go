package httpapi

import (
	"net/url"
	"strings"
	"testing"
)

// TestCustomFields_RenderOnTicketFormAndPersist covers RELEASE/v_3.0.0.md:
// CustomFieldDef rows scoped to a category render dynamically on the
// ticket-create form (via the category input's htmx change trigger) and the
// submitted values persist and show up on the ticket detail page.
func TestCustomFields_RenderOnTicketFormAndPersist(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/admin/custom-fields", url.Values{
		"category": {"network"}, "name": {"vlan"}, "label": {"VLAN ID"}, "type": {"text"}, "required": {"on"},
	})

	// The dynamic fragment endpoint the category field's hx-get hits.
	fragment := bodyString(t, admin.get("/custom-fields/for-category?category=network"))
	if !strings.Contains(fragment, `name="cf_vlan"`) || !strings.Contains(fragment, "VLAN ID") {
		t.Fatalf("expected the vlan field in the fragment, got: %s", fragment)
	}
	// A category with no defined fields renders nothing.
	empty := bodyString(t, admin.get("/custom-fields/for-category?category=nonexistent"))
	if strings.Contains(empty, "cf_") {
		t.Fatalf("expected no fields for an undefined category, got: %s", empty)
	}

	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Switch port down"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"},
		"category": {"network"}, "cf_vlan": {"42"},
	})

	body := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "<strong>vlan:</strong> 42") {
		t.Fatalf("expected the custom field value to render on the ticket detail page, got: %s", body)
	}
}

// TestCustomFields_CreationIsSystemAdminOnly re-confirms the RBAC change
// (custom field *definitions* moved off Manager's CapQueueOps) doesn't
// accidentally also gate *filling in* a custom field on ticket creation -
// any authenticated user can submit cf_* values, only defining them is
// SystemAdmin-only (covered separately in integration_service_test.go).
func TestCustomFields_CreationIsSystemAdminOnly(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/admin/custom-fields", url.Values{
		"category": {"network"}, "name": {"vlan"}, "label": {"VLAN ID"}, "type": {"text"},
	})
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")

	resp := eng.postForm("/tickets", url.Values{
		"title": {"Switch port down"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P2"},
		"category": {"network"}, "cf_vlan": {"7"},
	})
	if resp.StatusCode != 200 {
		t.Fatalf("engineer ticket create with a custom field value: got %d", resp.StatusCode)
	}
	resp.Body.Close()
	body := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "<strong>vlan:</strong> 7") {
		t.Fatal("expected the engineer-submitted custom field value to persist")
	}
}
