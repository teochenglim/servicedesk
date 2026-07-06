package service

import "servicedesk/internal/models"

type Action string

const (
	ActionAssign  Action = "assign"
	ActionPickup  Action = "pickup"
	ActionResolve Action = "resolve"
	ActionConfirm Action = "confirm"
	ActionReopen  Action = "reopen"
	ActionReject  Action = "reject"
)

// transitions implements the explicit state machine from DESIGN.md 3.1.2.
var transitions = map[models.TicketStatus]map[Action]models.TicketStatus{
	models.StatusNew: {
		ActionAssign: models.StatusInProgress,
		ActionPickup: models.StatusInProgress,
	},
	models.StatusInProgress: {
		ActionResolve: models.StatusResolved,
		ActionReject:  models.StatusRejected,
	},
	models.StatusResolved: {
		ActionConfirm: models.StatusClosed,
		ActionReopen:  models.StatusInProgress,
	},
	models.StatusClosed: {
		ActionReopen: models.StatusInProgress,
	},
}

// forcedNext models "Rejected -> Closed (forced)": landing on Rejected
// immediately cascades to Closed within the same transition call.
var forcedNext = map[models.TicketStatus]models.TicketStatus{
	models.StatusRejected: models.StatusClosed,
}

type TransitionError struct {
	From   models.TicketStatus
	Action Action
}

func (e *TransitionError) Error() string {
	return "invalid transition: " + string(e.From) + " -> " + string(e.Action)
}

// nextStatus resolves an action against the current status, following any
// forced cascades, and returns the full path of statuses visited (for audit logging).
func nextStatus(current models.TicketStatus, action Action) (final models.TicketStatus, path []models.TicketStatus, err error) {
	byAction, ok := transitions[current]
	if !ok {
		return "", nil, &TransitionError{From: current, Action: action}
	}
	next, ok := byAction[action]
	if !ok {
		return "", nil, &TransitionError{From: current, Action: action}
	}
	path = append(path, next)
	for {
		if fwd, ok := forcedNext[next]; ok {
			next = fwd
			path = append(path, next)
			continue
		}
		break
	}
	return next, path, nil
}
