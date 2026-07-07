package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestAttachment_UploadDownloadAndCustomerPrivacy exercises the full HTTP
// path: upload, thumbnail-worthy image sniffing, and the new requirement
// that one Customer's upload is invisible to another Customer even when
// both can see the same ticket (via watch), while staff always see it.
func TestAttachment_UploadDownloadAndCustomerPrivacy(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createOrg(t, admin, "Acme Corp")
	createUser(t, admin, "alice", "a@acme.com", "pass123", "Customer")
	createUser(t, admin, "bob", "b@acme.com", "pass123", "Customer")
	createUser(t, admin, "eng1", "e1@x.com", "pass123", "Engineer")
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"3"}}) // alice
	admin.mustPost(t, "/admin/orgs/1/members", url.Values{"user_id": {"4"}}) // bob

	alice := env.client()
	alice.mustLogin("Acme Corp", "alice", "pass123")
	alice.mustPost(t, "/tickets", url.Values{
		"title": {"Broken screen"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"hw"},
	})

	bob := env.client()
	bob.mustLogin("Acme Corp", "bob", "pass123")
	bob.postForm("/tickets/1/watch", nil).Body.Close() // bob can now see ticket #1, per the watch mechanism

	// Alice uploads a screenshot to her own ticket.
	resp := alice.postFile("/tickets/1/attachments", "screenshot.png", tiny1x1PNG)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alice upload: got %d", resp.StatusCode)
	}

	// Staff (engineer) sees it in the ticket detail page.
	eng := env.client()
	eng.mustLogin("", "eng1", "pass123")
	body := bodyString(t, eng.get("/tickets/1"))
	if !strings.Contains(body, "/attachments/1") {
		t.Fatal("engineer should see alice's attachment")
	}

	// Alice (the uploader) sees her own attachment.
	body = bodyString(t, alice.get("/tickets/1"))
	if !strings.Contains(body, "/attachments/1") {
		t.Fatal("alice should see her own attachment")
	}

	// Bob (a different Customer, watching the same ticket) must NOT see it,
	// even though he can see the ticket itself.
	body = bodyString(t, bob.get("/tickets/1"))
	if strings.Contains(body, "/attachments/1") {
		t.Fatal("bob must not see alice's attachment, even though he can watch the same ticket")
	}

	// Direct download attempts mirror the same rule.
	resp = bob.get("/attachments/1")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bob direct download: got %d, want 403", resp.StatusCode)
	}
	resp = alice.get("/attachments/1")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("alice direct download of her own upload: got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png (sniffed, not client-declared)", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "inline") {
		t.Errorf("Content-Disposition = %q, want inline for a sniffed image", cd)
	}
}

// TestAttachment_RejectsDisallowedType covers the upload-time allowlist.
func TestAttachment_RejectsDisallowedType(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"x"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"x"},
	})
	resp := admin.postFile("/tickets/1/attachments", "malware.exe", []byte("MZ-fake-binary"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("uploading a .exe: got %d, want 400", resp.StatusCode)
	}
}
