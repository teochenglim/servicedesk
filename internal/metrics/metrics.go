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

	// Stage-tracking overlay metrics (DESIGN/03 §3.1.2b): durations between
	// consecutive Detect/Ack/Mitigate/Resolve stage timestamps, observed at
	// write-time in internal/service/ticket.go. Buckets favor ticket-lifecycle
	// scale (minutes to a week) rather than DefBuckets' sub-second HTTP scale.
	ticketStageBuckets = []float64{60, 300, 900, 1800, 3600, 14400, 28800, 86400, 259200, 604800}

	MTTDSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "servicedesk_ticket_mttd_seconds",
		Help:    "Mean time to detect: ticket creation minus the underlying trigger time (near-zero unless Agent-backdated).",
		Buckets: ticketStageBuckets,
	})
	MTTASeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "servicedesk_ticket_mtta_seconds",
		Help:    "Mean time to ack: Detect to first pickup/assign.",
		Buckets: ticketStageBuckets,
	})
	MTTMSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "servicedesk_ticket_mttm_seconds",
		Help:    "Mean time to mitigate: Ack to a workaround being marked in place.",
		Buckets: ticketStageBuckets,
	})
	MTTRSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "servicedesk_ticket_mttr_seconds",
		Help:    "Mean time to resolve: Mitigate to root cause fixed.",
		Buckets: ticketStageBuckets,
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

// Flush makes statusRecorder itself satisfy http.Flusher (RELEASE/v_3.0.7.md).
// Embedding the http.ResponseWriter interface only promotes that interface's
// own methods (Write/Header/WriteHeader) - Flush belongs to the separate
// http.Flusher interface, so without this, sse.Hub.Handler's `w.(http.Flusher)`
// type assertion failed on every request wrapped by this middleware (i.e.
// every request, since it wraps the whole mux), and GET /events 500'd
// immediately with "streaming unsupported" instead of ever streaming.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
