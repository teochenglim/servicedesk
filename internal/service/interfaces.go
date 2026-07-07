package service

// EventPublisher fans out real-time updates to SSE-connected watchers (3.8).
type EventPublisher interface {
	Publish(ticketID int64, event string, payload any)
}

// WebhookDispatcher enqueues outbound webhook deliveries (3.6).
type WebhookDispatcher interface {
	Dispatch(event string, payload any)
}

// WorkflowTrigger fires admin-defined workflow rules / runbooks (3.5, 4).
type WorkflowTrigger interface {
	Trigger(triggerName string, ticketID int64, context map[string]any)
}

// AISummaryTrigger regenerates the AI Ticket Intelligence Panel (DESIGN/08
// §8.9) - implemented by *AISummaryService, kept as an interface so
// NoteService doesn't need a hard dependency, and so it can be nil (AI
// features disabled) with a plain nil-check at the call site.
type AISummaryTrigger interface {
	Regenerate(ticketID int64, triggeringNoteID *int64) error
}
