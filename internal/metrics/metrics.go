package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "servicedesk_http_requests_total",
		Help: "Total HTTP requests processed, by route/method/status.",
	}, []string{"route", "method", "status"})

	HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "servicedesk_http_request_duration_seconds",
		Help:    "HTTP request latency in seconds.",
		Buckets: prometheus.DefBuckets,
	}, []string{"route", "method"})

	TicketsCreatedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "servicedesk_tickets_created_total",
		Help: "Total tickets created.",
	})

	TicketTransitionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "servicedesk_ticket_transitions_total",
		Help: "Ticket state machine transitions, by action and resulting status.",
	}, []string{"action", "to_status"})

	NotesCreatedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "servicedesk_notes_created_total",
		Help: "Notes added to tickets, by visibility.",
	}, []string{"visibility"})

	WebhookDeliveriesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "servicedesk_webhook_deliveries_total",
		Help: "Webhook delivery attempts, by outcome.",
	}, []string{"outcome"})

	WorkflowTasksProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "servicedesk_workflow_tasks_processed_total",
		Help: "Workflow/runbook task steps processed, by resulting status.",
	}, []string{"status"})

	SSEConnectedClients = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "servicedesk_sse_connected_clients",
		Help: "Currently connected SSE clients.",
	})
)

// Middleware records request counts/latency labeled by route pattern (r.Pattern,
// populated by Go 1.22+'s ServeMux from the registered "METHOD /path" pattern).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)

		route := r.Pattern
		if route == "" {
			route = r.URL.Path
		}
		HTTPRequestsTotal.WithLabelValues(route, r.Method, strconv.Itoa(rec.status)).Inc()
		HTTPRequestDuration.WithLabelValues(route, r.Method).Observe(time.Since(start).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
