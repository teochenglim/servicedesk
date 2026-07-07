package httpapi

import (
	"strings"
	"testing"
	"time"

	"servicedesk/internal/models"
)

// TestComputeMTTxTrend_BucketsByResolvedDate covers RELEASE/v_3.0.0.md's
// MTTx trend chart: tickets resolved on different days land in different
// buckets, and a day with nothing resolved gets a zero point rather than
// being dropped (so every sparkline stays evenly spaced).
func TestComputeMTTxTrend_BucketsByResolvedDate(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	created := now.AddDate(0, 0, -4)
	detected := now.AddDate(0, 0, -3)
	resolvedToday := now
	resolvedYesterday := now.AddDate(0, 0, -1)

	tickets := []models.Ticket{
		{CreatedAt: created, DetectedAt: &detected, ResolvedAt: &resolvedToday},
		{CreatedAt: created, DetectedAt: &detected, ResolvedAt: &resolvedYesterday},
	}

	points := computeMTTxTrend(tickets, 3, now)
	if len(points) != 3 {
		t.Fatalf("points = %d, want 3", len(points))
	}
	// Oldest first: now-2, now-1, now.
	if points[0].MTTDMin != 0 {
		t.Errorf("day with no resolutions should be a zero point, got %+v", points[0])
	}
	if points[1].MTTDMin == 0 {
		t.Errorf("expected a non-zero MTTD for the day the 'yesterday' ticket resolved, got %+v", points[1])
	}
	if points[2].MTTDMin == 0 {
		t.Errorf("expected a non-zero MTTD for the day the 'today' ticket resolved, got %+v", points[2])
	}
	if points[0].Date != now.AddDate(0, 0, -2).Format("2006-01-02") {
		t.Errorf("Date = %q, want the oldest day in the window first", points[0].Date)
	}
}

func TestSparklineSVG_EmptyAndAllZeroDontPanic(t *testing.T) {
	if got := sparklineSVG(nil, "red"); got != "" {
		t.Errorf("nil input should render nothing, got %q", got)
	}
	svg := sparklineSVG([]float64{0, 0, 0}, "var(--tw-teal-600)")
	if !strings.Contains(string(svg), "<svg") || !strings.Contains(string(svg), "<polyline") {
		t.Errorf("expected a flat-baseline sparkline for all-zero data, got %q", svg)
	}
}

func TestSparklineSVG_LastPointCarriesTheAccentColor(t *testing.T) {
	svg := string(sparklineSVG([]float64{1, 5, 3, 9}, "var(--tw-purple-500)"))
	if !strings.Contains(svg, `fill="var(--tw-purple-500)"`) {
		t.Errorf("expected the end-marker circle to carry the accent color, got %q", svg)
	}
}
