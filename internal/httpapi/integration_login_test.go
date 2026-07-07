package httpapi

import (
	"net/url"
	"strings"
	"testing"

	"servicedesk/internal/demo"
	"servicedesk/internal/logging"
)

// TestLogin_DemoPersonaPickerOnlyShowsInDemoMode covers RELEASE/v_3.0.0.md's
// demo persona login picker: hidden by default, visible once DemoMode is on.
func TestLogin_DemoPersonaPickerOnlyShowsInDemoMode(t *testing.T) {
	env := newTestEnv(t)
	c := env.client()

	body := bodyString(t, c.get("/login"))
	if strings.Contains(body, "Log in as (demo)") {
		t.Fatal("picker must not render when DemoMode is off")
	}

	env.server.SetDemoMode(true)
	body = bodyString(t, c.get("/login"))
	if !strings.Contains(body, "Log in as (demo)") {
		t.Fatal("picker should render once DemoMode is on")
	}
	for _, want := range []string{"demoLogin('demo.customer1', 'demo1234', 'Acme Corp')", "demoLogin('demo.engineer1', 'demo1234', '')",
		"demoLogin('demo.admin', 'demo1234', '')", "demoLogin('admin', 'admin123', '')"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected picker button wired to %q", want)
		}
	}
}

// TestLogin_DemoPersonaCredentialsActuallyLogIn confirms each set of
// credentials the picker fills in is real - a JS-driven click can't be
// exercised by a plain HTTP client, so this drives the same /login POST the
// button's demoLogin() would submit.
func TestLogin_DemoPersonaCredentialsActuallyLogIn(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123") // seeds nothing itself, just confirms this credential pair works

	if err := demo.Seed(env.db, logging.New("error")); err != nil {
		t.Fatalf("demo.Seed: %v", err)
	}

	for _, tc := range []struct{ org, username, password string }{
		{"Acme Corp", "demo.customer1", "demo1234"},
		{"", "demo.engineer1", "demo1234"},
		{"", "demo.admin", "demo1234"},
	} {
		c := env.client()
		resp := c.postForm("/login", url.Values{"org": {tc.org}, "username": {tc.username}, "password": {tc.password}})
		resp.Body.Close()
		if !c.hasSessionCookie() {
			t.Errorf("login(%q,%q) did not set a session cookie", tc.org, tc.username)
		}
	}
}
