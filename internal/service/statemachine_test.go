package service

import (
	"testing"

	"servicedesk/internal/models"
)

// TestStateMachine_ValidTransitions covers the explicit state machine from
// DESIGN.md 3.1.2, including the "Rejected -> Closed (forced)" cascade.
func TestStateMachine_ValidTransitions(t *testing.T) {
	cases := []struct {
		name   string
		from   models.TicketStatus
		action Action
		want   models.TicketStatus
		path   []models.TicketStatus
	}{
		{"new_assign_to_in_progress", models.StatusNew, ActionAssign, models.StatusInProgress, []models.TicketStatus{models.StatusInProgress}},
		{"new_pickup_to_in_progress", models.StatusNew, ActionPickup, models.StatusInProgress, []models.TicketStatus{models.StatusInProgress}},
		{"in_progress_resolve", models.StatusInProgress, ActionResolve, models.StatusResolved, []models.TicketStatus{models.StatusResolved}},
		{"resolved_confirm_to_closed", models.StatusResolved, ActionConfirm, models.StatusClosed, []models.TicketStatus{models.StatusClosed}},
		{"resolved_reopen_to_in_progress", models.StatusResolved, ActionReopen, models.StatusInProgress, []models.TicketStatus{models.StatusInProgress}},
		{"closed_reopen_to_in_progress", models.StatusClosed, ActionReopen, models.StatusInProgress, []models.TicketStatus{models.StatusInProgress}},
		{
			"in_progress_reject_forces_closed", models.StatusInProgress, ActionReject, models.StatusClosed,
			[]models.TicketStatus{models.StatusRejected, models.StatusClosed},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, path, err := nextStatus(c.from, c.action)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("nextStatus(%s, %s) = %s, want %s", c.from, c.action, got, c.want)
			}
			if len(path) != len(c.path) {
				t.Fatalf("path = %v, want %v", path, c.path)
			}
			for i := range path {
				if path[i] != c.path[i] {
					t.Errorf("path[%d] = %s, want %s", i, path[i], c.path[i])
				}
			}
		})
	}
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	cases := []struct {
		name   string
		from   models.TicketStatus
		action Action
	}{
		{"new_cannot_resolve_directly", models.StatusNew, ActionResolve},
		{"closed_cannot_resolve", models.StatusClosed, ActionResolve},
		{"resolved_cannot_pickup", models.StatusResolved, ActionPickup},
		{"in_progress_cannot_confirm", models.StatusInProgress, ActionConfirm},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, err := nextStatus(c.from, c.action); err == nil {
				t.Errorf("nextStatus(%s, %s) expected an error, got none", c.from, c.action)
			}
		})
	}
}
