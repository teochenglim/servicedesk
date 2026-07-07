package sla

import (
	"testing"
	"time"
)

func TestEvaluate_FallsBackToDefaultTable(t *testing.T) {
	var empty Table
	minutes, ok := empty.Evaluate("P1", "")
	if !ok || minutes != 240 {
		t.Fatalf("empty table P1: got (%d, %v), want (240, true)", minutes, ok)
	}
}

func TestEvaluate_WildcardCategoryRow(t *testing.T) {
	tbl := Table{{Priority: "P2", Minutes: 90}}
	minutes, ok := tbl.Evaluate("P2", "network")
	if !ok || minutes != 90 {
		t.Fatalf("P2/network against wildcard P2 row: got (%d, %v), want (90, true)", minutes, ok)
	}
}

func TestEvaluate_CategorySpecificRowBeatsWildcard(t *testing.T) {
	tbl := Table{
		{Priority: "P1", Minutes: 240},
		{Priority: "P1", Category: "security", Minutes: 30},
	}
	minutes, ok := tbl.Evaluate("P1", "security")
	if !ok || minutes != 30 {
		t.Fatalf("P1/security: got (%d, %v), want (30, true) - specific row should win", minutes, ok)
	}
	// A different category on the same priority still falls back to the wildcard row.
	minutes, ok = tbl.Evaluate("P1", "network")
	if !ok || minutes != 240 {
		t.Fatalf("P1/network: got (%d, %v), want (240, true)", minutes, ok)
	}
}

func TestDueAt_AddsMinutesToFrom(t *testing.T) {
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tbl := Table{{Priority: "P3", Minutes: 60}}
	due := tbl.DueAt("P3", "", from)
	if due == nil || !due.Equal(from.Add(time.Hour)) {
		t.Fatalf("DueAt: got %v, want %v", due, from.Add(time.Hour))
	}
}

func TestDueAt_UnknownPriorityReturnsNil(t *testing.T) {
	var empty Table
	if due := empty.DueAt("P9", "", time.Now()); due != nil {
		t.Fatalf("unknown priority should return nil due time, got %v", due)
	}
}
