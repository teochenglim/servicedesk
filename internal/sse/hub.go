package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"servicedesk/internal/middleware"
	"servicedesk/internal/repo"
)

type Message struct {
	TicketID int64  `json:"ticket_id"`
	Event    string `json:"event"`
	Payload  any    `json:"payload"`
}

// Hub implements service.EventPublisher and fans out ticket events to the
// watchers/assignee of that ticket (DESIGN.md 3.8), keyed by user ID.
type Hub struct {
	mu          sync.Mutex
	subscribers map[int64]map[chan Message]struct{}
	closing     chan struct{}

	watchers *repo.WatcherRepo
	tickets  *repo.TicketRepo
}

func NewHub(watchers *repo.WatcherRepo, tickets *repo.TicketRepo) *Hub {
	return &Hub{
		subscribers: make(map[int64]map[chan Message]struct{}),
		closing:     make(chan struct{}),
		watchers:    watchers,
		tickets:     tickets,
	}
}

// Close signals every active Handler stream to return immediately. http.Server.Shutdown
// only waits for handlers to return on their own - it never cancels an in-flight request's
// context - so a long-lived SSE connection would otherwise hold the drain open until the
// caller's shutdown timeout expires (see RELEASE/v_3.0.9.md). Call this before
// httpSrv.Shutdown so the drain returns as soon as streams notice, not after the timeout.
func (h *Hub) Close() {
	close(h.closing)
}

func (h *Hub) subscribe(userID int64) (chan Message, func()) {
	ch := make(chan Message, 16)
	h.mu.Lock()
	if h.subscribers[userID] == nil {
		h.subscribers[userID] = make(map[chan Message]struct{})
	}
	h.subscribers[userID][ch] = struct{}{}
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		delete(h.subscribers[userID], ch)
		if len(h.subscribers[userID]) == 0 {
			delete(h.subscribers, userID)
		}
		h.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}

// Publish implements service.EventPublisher.
func (h *Hub) Publish(ticketID int64, event string, payload any) {
	recipients := map[int64]struct{}{}
	if ids, err := h.watchers.ListUserIDsForTicket(ticketID); err == nil {
		for _, id := range ids {
			recipients[id] = struct{}{}
		}
	}
	if t, err := h.tickets.Get(ticketID); err == nil && t.AssigneeID != nil {
		recipients[*t.AssigneeID] = struct{}{}
	}

	msg := Message{TicketID: ticketID, Event: event, Payload: payload}
	h.mu.Lock()
	defer h.mu.Unlock()
	for uid := range recipients {
		for ch := range h.subscribers[uid] {
			select {
			case ch <- msg:
			default: // slow consumer; drop rather than block publishers
			}
		}
	}
}

// Handler serves GET /events as an EventSource stream scoped to the caller's watched/assigned tickets.
func (h *Hub) Handler(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	if claims == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := h.subscribe(claims.UserID)
	defer cancel()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-h.closing:
			return
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case msg := <-ch:
			data, _ := json.Marshal(msg.Payload)
			fmt.Fprintf(w, "event: %s\ndata: {\"ticket_id\":%d,\"payload\":%s}\n\n", msg.Event, msg.TicketID, data)
			flusher.Flush()
		}
	}
}
