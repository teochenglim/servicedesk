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
