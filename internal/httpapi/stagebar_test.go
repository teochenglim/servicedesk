package httpapi

import (
	"strings"
	"testing"
	"time"

	"servicedesk/internal/models"
)

func TestStageProgress_Detect(t *testing.T) {
	now := time.Now()
	created := now.Add(-30 * time.Minute)
	tk := &models.Ticket{Status: models.StatusNew, CreatedAt: created, DetectedAt: &created}

	bar := stageProgress(tk, now, false)
	if !bar.Applicable || bar.CurrentIdx != 0 {
		t.Fatalf("expected Detect stage (idx 0), got %+v", bar)
	}
	if bar.Closed {
		t.Error("new ticket should not report Closed")
	}
}

func TestStageProgress_AckMitigateResolve(t *testing.T) {
	now := time.Now()
	detected := now.Add(-3 * time.Hour)
	acked := now.Add(-2 * time.Hour)
	mitigated := now.Add(-1 * time.Hour)
	resolved := now.Add(-10 * time.Minute)

	ackOnly := &models.Ticket{Status: models.StatusInProgress, DetectedAt: &detected, AckedAt: &acked}
	if bar := stageProgress(ackOnly, now, false); bar.CurrentIdx != 1 {
		t.Errorf("Ack-only ticket: CurrentIdx = %d, want 1", bar.CurrentIdx)
	}

	mitigatedTk := &models.Ticket{Status: models.StatusInProgress, DetectedAt: &detected, AckedAt: &acked, MitigatedAt: &mitigated}
	if bar := stageProgress(mitigatedTk, now, false); bar.CurrentIdx != 2 {
		t.Errorf("Mitigated ticket: CurrentIdx = %d, want 2", bar.CurrentIdx)
	}

	resolvedTk := &models.Ticket{
		Status: models.StatusResolved, DetectedAt: &detected, AckedAt: &acked,
		MitigatedAt: &mitigated, ResolvedAt: &resolved,
	}
	bar := stageProgress(resolvedTk, now, false)
	if bar.CurrentIdx != 3 {
		t.Errorf("Resolved ticket: CurrentIdx = %d, want 3", bar.CurrentIdx)
	}
	if bar.Closed {
		t.Error("Resolved-but-not-yet-Closed ticket should not report Closed")
	}

	closedTk := resolvedTk
	closedTk.Status = models.StatusClosed
	if bar := stageProgress(closedTk, now, false); !bar.Closed || bar.CurrentIdx != 3 {
		t.Errorf("Closed ticket: got %+v, want Closed=true CurrentIdx=3", bar)
	}
}

// TestStageProgress_ReopenResetsToLatestMilestone verifies the bar resets to
// whichever of Ack/Mitigate is the latest non-nil milestone after a reopen
// clears ResolvedAt - this is exactly the behavior DESIGN/08 §8.2 describes,
// and it falls out of stageProgress's plain nil-checks with no special-casing.
func TestStageProgress_ReopenResetsToLatestMilestone(t *testing.T) {
	now := time.Now()
	acked := now.Add(-2 * time.Hour)
	mitigated := now.Add(-1 * time.Hour)

	// Reopened after being mitigated (ResolvedAt cleared by Transition, per ticket.go).
	reopenedAfterMitigate := &models.Ticket{
		Status: models.StatusInProgress, AckedAt: &acked, MitigatedAt: &mitigated, ReopenCount: 1,
	}
	if bar := stageProgress(reopenedAfterMitigate, now, false); bar.CurrentIdx != 2 {
		t.Errorf("reopened-after-mitigate: CurrentIdx = %d, want 2 (Mitigate)", bar.CurrentIdx)
	}

	// Reopened before ever being mitigated.
	reopenedAfterAckOnly := &models.Ticket{Status: models.StatusInProgress, AckedAt: &acked, ReopenCount: 1}
	if bar := stageProgress(reopenedAfterAckOnly, now, false); bar.CurrentIdx != 1 {
		t.Errorf("reopened-after-ack-only: CurrentIdx = %d, want 1 (Ack)", bar.CurrentIdx)
	}
}

// TestStageProgress_RejectedHasNoBar covers the locked product decision that
// Rejected sits outside the bar - and the fact that Rejected never actually
// rests as Ticket.Status (statemachine.go cascades it straight to Closed), so
// this is inferred from Closed-without-ever-having-a-ResolvedAt.
func TestStageProgress_RejectedHasNoBar(t *testing.T) {
	acked := time.Now().Add(-time.Hour)
	rejected := &models.Ticket{Status: models.StatusClosed, AckedAt: &acked, ResolvedAt: nil}
	if bar := stageProgress(rejected, time.Now(), false); bar.Applicable {
		t.Errorf("rejected-then-closed ticket should not be Applicable: %+v", bar)
	}

	// A genuinely resolved-then-closed ticket must still show the bar.
	resolved := time.Now().Add(-time.Minute)
	confirmedClosed := &models.Ticket{Status: models.StatusClosed, AckedAt: &acked, ResolvedAt: &resolved}
	if bar := stageProgress(confirmedClosed, time.Now(), false); !bar.Applicable {
		t.Error("resolved-then-closed ticket should still show the bar")
	}
}

// TestStageProgress_PlainDropsJargon covers DESIGN/08 §8.4: the Customer-facing
// variant must not surface "Ack"/"Mitigate" wording anywhere in the stage
// names or the label.
func TestStageProgress_PlainDropsJargon(t *testing.T) {
	now := time.Now()
	acked := now.Add(-time.Hour)
	tk := &models.Ticket{Status: models.StatusInProgress, AckedAt: &acked}

	bar := stageProgress(tk, now, true)
	for _, name := range bar.Stages {
		if name == "Ack" || name == "Mitigate" {
			t.Errorf("plain stage names must not contain jargon, got %v", bar.Stages)
		}
	}
	if strings.Contains(strings.ToLower(bar.StageLabel), "ack") {
		t.Errorf("plain label must not mention ack: %q", bar.StageLabel)
	}
}

func TestOrdinal(t *testing.T) {
	cases := map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 13: "13th", 21: "21st", 22: "22nd", 23: "23rd", 101: "101st"}
	for n, want := range cases {
		if got := ordinal(n); got != want {
			t.Errorf("ordinal(%d) = %q, want %q", n, got, want)
		}
	}
}
