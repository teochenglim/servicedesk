package httpapi

import (
	"fmt"
	"time"

	"servicedesk/internal/models"
)

// stageBarData is the shared Ticket Progress Bar view-model (DESIGN/08 §8.2),
// built from the additive stage-tracking columns (DESIGN/03 §3.1.2b). Only
// one dot is ever filled - the current stage - regardless of stages already
// passed; every diagram in the design doc shows exactly one filled dot, not
// a trail of checkmarks, so CurrentIdx is the only position state needed.
type stageBarData struct {
	Applicable  bool // false for Rejected - it sits outside the bar entirely
	Stages      []string
	CurrentIdx  int
	Closed      bool
	StageLabel  string
	ReopenCount int
}

// stageProgress derives the current stage purely from which timestamp is
// most recently set - it deliberately doesn't need special-case logic for
// Reopen, because Transition already clears ResolvedAt (and never touches
// AckedAt/MitigatedAt) on reopen, so the bar naturally recomputes back to
// whichever of Ack/Mitigate is the latest non-nil milestone.
func stageProgress(t *models.Ticket, now time.Time) stageBarData {
	// StatusRejected never actually rests as the persisted Status: statemachine.go's
	// forcedNext cascades it straight to Closed within the same Transition call
	// (see TransitionError/forcedNext), so "was this ticket rejected" has to be
	// inferred rather than read off Status directly. A ticket only reaches Closed
	// without ever passing through Resolve if it got there via the reject cascade -
	// Confirm (the other path to Closed) always follows Resolve, which always
	// stamps ResolvedAt first. This holds across reopen cycles too, since Reopen
	// clears ResolvedAt and a genuine second resolution re-stamps it.
	if t.Status == models.StatusClosed && t.ResolvedAt == nil {
		return stageBarData{Applicable: false}
	}

	var idx int
	var label string
	switch {
	case t.ResolvedAt != nil:
		idx = 3
		label = "Resolved " + humanDuration(now.Sub(*t.ResolvedAt)) + " ago"
	case t.MitigatedAt != nil:
		idx = 2
		label = "Mitigated " + humanDuration(now.Sub(*t.MitigatedAt)) + " ago · working on root cause"
	case t.AckedAt != nil:
		idx = 1
		label = humanDuration(now.Sub(*t.AckedAt)) + " since ack"
	default:
		idx = 0
		since := t.CreatedAt
		if t.DetectedAt != nil {
			since = *t.DetectedAt
		}
		label = humanDuration(now.Sub(since)) + " (waiting for ack)"
	}

	return stageBarData{
		Applicable:  true,
		Stages:      []string{"Detect", "Ack", "Mitigate", "Resolve"},
		CurrentIdx:  idx,
		Closed:      t.Status == models.StatusClosed,
		StageLabel:  label,
		ReopenCount: t.ReopenCount,
	}
}

// ordinal renders 1 -> "1st", 2 -> "2nd", 3 -> "3rd", 4 -> "4th", etc., for
// the progress bar's "reopened, Nth time" tag.
func ordinal(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return fmt.Sprintf("%dth", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%dst", n)
	case 2:
		return fmt.Sprintf("%dnd", n)
	case 3:
		return fmt.Sprintf("%drd", n)
	default:
		return fmt.Sprintf("%dth", n)
	}
}
