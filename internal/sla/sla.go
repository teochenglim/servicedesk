// Package sla evaluates SLA due-by times against a small rule table, kept
// separate from internal/service so the evaluation logic (and its rule
// shape) can grow - more dimensions, breach escalation, business-hours-aware
// clocks - without service/queue.go or service/ticket.go needing to change,
// only the Table they hold (DESIGN/08 §8.6).
package sla

import "time"

// Rule is one row of an SLA table: how long a ticket has before it breaches.
// Category "" is a wildcard row, matching any category for that Priority -
// every queue starts with only wildcard rows (Priority-only), but a row with
// Category set (e.g. "incident type") is a more specific match, so a future
// per-category matrix is just adding rows, not a schema or code change.
type Rule struct {
	Priority string `json:"priority"`
	Category string `json:"category,omitempty"`
	Minutes  int    `json:"minutes"`
}

// Table is a queue's configured SLA rules, evaluated most-specific-first.
type Table []Rule

// DefaultTable backstops any (priority, category) combination a queue's own
// Table doesn't cover.
var DefaultTable = Table{
	{Priority: "P1", Minutes: 4 * 60},
	{Priority: "P2", Minutes: 8 * 60},
	{Priority: "P3", Minutes: 24 * 60},
	{Priority: "P4", Minutes: 72 * 60},
}

// Evaluate resolves (priority, category) to a target duration in minutes:
// an exact Priority+Category match wins, then Priority with a wildcard
// Category, then DefaultTable for that priority.
func (t Table) Evaluate(priority, category string) (minutes int, ok bool) {
	if category != "" {
		for _, r := range t {
			if r.Priority == priority && r.Category == category {
				return r.Minutes, true
			}
		}
	}
	for _, r := range t {
		if r.Priority == priority && r.Category == "" {
			return r.Minutes, true
		}
	}
	for _, r := range DefaultTable {
		if r.Priority == priority {
			return r.Minutes, true
		}
	}
	return 0, false
}

// DueAt computes the SLA due-by time for a ticket created at from.
func (t Table) DueAt(priority, category string, from time.Time) *time.Time {
	minutes, ok := t.Evaluate(priority, category)
	if !ok {
		return nil
	}
	due := from.Add(time.Duration(minutes) * time.Minute)
	return &due
}
