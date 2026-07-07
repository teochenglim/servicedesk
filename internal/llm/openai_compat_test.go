package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPClient_CompleteParsesChoice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", got)
		}
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "qwen3:8b" {
			t.Errorf("model = %q, want qwen3:8b", req.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "hello back"}}},
		})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "test-key", "qwen3:8b")
	got, err := c.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got != "hello back" {
		t.Errorf("Complete = %q, want %q", got, "hello back")
	}
}

// TestHTTPClient_NoAPIKeyOmitsAuthHeader covers the Ollama default: no
// Authorization header at all when APIKey is empty, rather than "Bearer ".
func TestHTTPClient_NoAPIKeyOmitsAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty (no API key configured)", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "", "qwen3:8b")
	if _, err := c.Complete(context.Background(), nil); err != nil {
		t.Fatalf("Complete: %v", err)
	}
}

func TestHTTPClient_APIErrorSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "rate limited"}})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "k", "m")
	_, err := c.Complete(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected rate-limited error, got %v", err)
	}
}

func TestFakeClient_RecordsCallsAndReturnsCanned(t *testing.T) {
	f := &FakeClient{Response: "canned"}
	got, err := f.Complete(context.Background(), []Message{{Role: "system", Content: "x"}})
	if err != nil || got != "canned" {
		t.Fatalf("Complete = (%q, %v), want (canned, nil)", got, err)
	}
	if len(f.Calls) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(f.Calls))
	}
}
