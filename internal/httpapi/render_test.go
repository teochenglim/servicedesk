package httpapi

import (
	"testing"
	"time"
)

func timePtr(t time.Time) *time.Time { return &t }

func TestSlaStateAt(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		due  *time.Time
		want string
	}{
		{"nil due", nil, ""},
		{"far future is ok", timePtr(now.Add(5 * time.Hour)), "ok"},
		{"within 2h is warning", timePtr(now.Add(90 * time.Minute)), "warning"},
		{"past due is breach", timePtr(now.Add(-1 * time.Hour)), "breach"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := slaStateAt(c.due, now); got != c.want {
				t.Fatalf("slaStateAt() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSlaLabelAt(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	due := timePtr(now.Add(2*time.Hour + 15*time.Minute))
	if got := slaLabelAt(due, now); got != "2h 15m left" {
		t.Fatalf("slaLabelAt() = %q, want %q", got, "2h 15m left")
	}
	past := timePtr(now.Add(-90 * time.Minute))
	if got := slaLabelAt(past, now); got != "Breached 1h 30m ago" {
		t.Fatalf("slaLabelAt() = %q, want %q", got, "Breached 1h 30m ago")
	}
	if got := slaLabelAt(nil, now); got != "" {
		t.Fatalf("slaLabelAt(nil) = %q, want empty", got)
	}
}
