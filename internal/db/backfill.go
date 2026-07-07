package db

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"

	"servicedesk/internal/models"
)

// eventLogDetails is the union of the two EventLog.Details shapes relevant to
// backfilling stage timestamps (service.TicketService.assign/Transition):
// {"assignee_id":..., "action":"pickup|assign"} for "assigned" events, and
// {"from":..., "to":..., "action":"...", "reason":"..."} for "status_changed" events.
type eventLogDetails struct {
	To     string `json:"to"`
	Action string `json:"action"`
}

// backfillStageTimestamps is a one-time data fix for tickets created before
// the stage-tracking overlay (DESIGN/03 §3.1.2b) existed. DetectedAt IS NULL
// is used as the "not yet migrated" marker - going forward, the app always
// sets it at creation time, so a ticket only ever needs this once. MitigatedAt
// is deliberately left null: no ticket_mitigated events exist in history
// before this feature shipped, so there's nothing to derive it from.
func backfillStageTimestamps(gdb *gorm.DB) error {
	var tickets []models.Ticket
	if err := gdb.Where("detected_at IS NULL").Find(&tickets).Error; err != nil {
		return err
	}

	for _, t := range tickets {
		var events []models.EventLog
		if err := gdb.Where("ticket_id = ?", t.ID).Order("created_at ASC").Find(&events).Error; err != nil {
			return err
		}

		var ackedAt, resolvedAt *time.Time
		reopenCount := 0
		for _, ev := range events {
			switch ev.Event {
			case "assigned":
				if ackedAt == nil {
					ts := ev.CreatedAt
					ackedAt = &ts
				}
			case "status_changed":
				var d eventLogDetails
				if err := json.Unmarshal([]byte(ev.Details), &d); err != nil {
					continue
				}
				if d.To == string(models.StatusResolved) {
					ts := ev.CreatedAt
					resolvedAt = &ts
				}
				if d.Action == "reopen" {
					resolvedAt = nil
					reopenCount++
				}
			}
		}

		updates := map[string]any{
			"detected_at":  t.CreatedAt,
			"acked_at":     ackedAt,
			"resolved_at":  resolvedAt,
			"reopen_count": reopenCount,
		}
		if err := gdb.Model(&models.Ticket{}).Where("id = ?", t.ID).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
