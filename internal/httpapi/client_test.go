package httpapi

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// hasSessionCookie reports whether login actually set the sd_token cookie -
// a failed login re-renders the same 200 page, so status code alone can't tell success from failure.
func (c *client) hasSessionCookie() bool {
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "sd_token" {
			return true
		}
	}
	return false
}

// client is a small helper around http.Client for driving the real HTTP
// server the way a browser would (cookie jar included), so tests read like
// the manual curl smoke tests they replace.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *client) login(org, username, password string) *http.Response {
	c.t.Helper()
	return c.postForm("/login", url.Values{"org": {org}, "username": {username}, "password": {password}})
}

// mustLogin logs in and fails the test immediately if it didn't redirect
// (i.e. wasn't accepted), so callers can assume the session cookie is set.
func (c *client) mustLogin(org, username, password string) {
	c.t.Helper()
	resp := c.login(org, username, password)
	defer resp.Body.Close()
	// A rejected login re-renders the same 200 login page (see handleLogin),
	// so status code alone can't distinguish success from failure - check
	// that the session cookie actually got set.
	if resp.StatusCode != http.StatusOK || !c.hasSessionCookie() {
		body, _ := io.ReadAll(resp.Body)
		c.t.Fatalf("login(%q,%q) failed: status=%d body=%s", org, username, resp.StatusCode, body)
	}
}

func (c *client) get(path string) *http.Response {
	c.t.Helper()
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		c.t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func (c *client) postForm(path string, form url.Values) *http.Response {
	c.t.Helper()
	resp, err := c.http.PostForm(c.base+path, form)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

// postFile uploads a single file field named "file" as multipart/form-data,
// following redirects like postForm - used for attachment upload tests.
func (c *client) postFile(path, filename string, data []byte) *http.Response {
	c.t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		c.t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(data); err != nil {
		c.t.Fatalf("write form file: %v", err)
	}
	if err := w.Close(); err != nil {
		c.t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.base+path, &buf)
	if err != nil {
		c.t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func (c *client) postFormNoRedirect(path string, form url.Values) *http.Response {
	c.t.Helper()
	req, err := http.NewRequest(http.MethodPost, c.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		c.t.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	noRedirect := &http.Client{
		Jar:           c.http.Jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		c.t.Fatalf("POST %s: %v", path, err)
	}
	return resp
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
