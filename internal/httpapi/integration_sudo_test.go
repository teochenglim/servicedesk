package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestAdminIndex_RequiresSystemAdmin locks in a real regression caught during
// manual smoke-testing this phase: the reshaped /admin home (DESIGN/08 §8.3)
// shows every user's role plus one-tap "Sudo as" buttons, so it must be
// SystemAdmin-only, not "any staff" like the old flat nav-link page was.
func TestAdminIndex_RequiresSystemAdmin(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")

	for _, u := range []struct{ username, password string }{{"qadmin", "pass123"}, {"eng1", "pass123"}} {
		c := env.client()
		c.mustLogin("", u.username, u.password)
		resp := c.get("/admin")
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s GET /admin: got %d, want 403", u.username, resp.StatusCode)
		}
	}

	body := bodyString(t, admin.get("/admin"))
	if !strings.Contains(body, "System administration") {
		t.Fatal("expected real SystemAdmin to see the admin home")
	}
}

// TestSudo_StartActAsAndStop covers the core Sudo-as mechanics (DESIGN/02
// §2.5): the sudo'd session acts with the target's identity, every audit row
// attributes to the target with SudoByID recording the real admin, and
// stopping the session restores the real admin's own identity. Sudo-as no
// longer exists to *grant* capabilities SystemAdmin lacks (RELEASE/v_3.0.1.md:
// "SystemAdmin is the entire servicedesk" holds every capability directly)
// - it's for acting *as a specific other human*, e.g. so an action's audit
// trail and identity-scoped views (like the acting-as banner below) reflect
// that person, not the admin covering for them.
func TestSudo_StartActAsAndStop(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager") // id 3

	// Start sudo as qadmin (Manager).
	admin.mustPost(t, "/admin/users/3/sudo/start", nil)

	// The sudo'd session acts as qadmin.
	admin.mustPost(t, "/queues/1/sla", url.Values{
		"minutes_P1": {"10"}, "minutes_P2": {"20"}, "minutes_P3": {"30"}, "minutes_P4": {"40"},
	})

	// The acting-as banner is present, attributed correctly.
	body := bodyString(t, admin.get("/manager"))
	if !strings.Contains(body, "Acting as qadmin") || !strings.Contains(body, "sudo'd in by admin") {
		t.Fatal("expected the persistent sudo banner with correct attribution")
	}

	// Stop the session and confirm it's restored: GET /admin (SystemAdmin-only) works again.
	admin.mustPost(t, "/admin/sudo/stop", nil)
	resp := admin.get("/admin")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("after sudo stop, admin GET /admin: got %d, want 200 (identity restored)", resp.StatusCode)
	}

	// Audit trail: both events attribute to qadmin (the target), with SudoByID = admin.
	auditBody := bodyString(t, admin.get("/admin/audit?sudo_only=on"))
	if !strings.Contains(auditBody, "sudo_started") || !strings.Contains(auditBody, "sudo_stopped") {
		t.Fatal("expected both sudo_started and sudo_stopped in the sudo-only audit view")
	}
	if !strings.Contains(auditBody, "qadmin") || !strings.Contains(auditBody, "sudo'd in by admin") {
		t.Fatal("expected audit rows attributed to qadmin, sudo'd in by admin")
	}
}

// TestSudo_RequiresCapSudoAndRejectsSelf covers the RBAC edges around
// starting a session: only CapSudo (SystemAdmin) can start one, and sudo'ing
// into your own account is rejected as meaningless.
func TestSudo_RequiresCapSudoAndRejectsSelf(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer") // id 3

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")
	resp := eng.postFormNoRedirect("/admin/users/2/sudo/start", nil) // admin is id 2
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("Engineer sudo start: got %d, want 403", resp.StatusCode)
	}

	resp = admin.postFormNoRedirect("/admin/users/2/sudo/start", nil) // admin sudo'ing as themself
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("admin sudo into own account: got %d, want 400", resp.StatusCode)
	}
}

// TestSudo_StopWithoutActiveSessionFails ensures "return to ServiceDeskAdmin"
// can't be called outside an actual sudo session.
func TestSudo_StopWithoutActiveSessionFails(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	resp := admin.postFormNoRedirect("/admin/sudo/stop", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("sudo stop without an active session: got %d, want 400", resp.StatusCode)
	}
}

// TestUserRole_UpdateIsAuditLogged covers the "set/change user roles" bullet
// of ServiceDeskAdmin's control surface (DESIGN/08 §8.3): SystemAdmin-only,
// and the change itself shows up in the audit trail.
func TestUserRole_UpdateIsAuditLogged(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer") // id 3

	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")
	resp := eng.postFormNoRedirect("/admin/users/3/role", url.Values{"role": {"Manager"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-admin role change: got %d, want 403", resp.StatusCode)
	}

	admin.mustPost(t, "/admin/users/3/role", url.Values{"role": {"Manager"}})

	body := bodyString(t, admin.get("/admin/users"))
	if !strings.Contains(body, `value="Manager" selected`) {
		t.Fatal("expected eng1's role to now show as Manager")
	}

	auditBody := bodyString(t, admin.get("/admin/audit?event=role_changed"))
	if !strings.Contains(auditBody, "role_changed") || !strings.Contains(auditBody, "eng1") {
		t.Fatal("expected the role change to appear in the audit log")
	}
}
