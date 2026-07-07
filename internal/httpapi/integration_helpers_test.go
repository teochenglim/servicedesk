package httpapi

import (
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// tiny1x1PNG is a minimal valid 1x1 transparent PNG, used to exercise the
// real http.DetectContentType image-sniffing path in attachment tests.
var tiny1x1PNG = func() []byte {
	b, _ := base64.StdEncoding.DecodeString(
		"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=")
	return b
}()

// --- shared helpers for every integration_*_test.go file -----------------

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
