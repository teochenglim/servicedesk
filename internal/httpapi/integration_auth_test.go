package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

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
