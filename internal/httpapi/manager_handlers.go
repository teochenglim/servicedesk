package httpapi

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strings"
	"time"

	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// managerFetchLimit bounds the single fetch-everything-and-aggregate-in-Go
// query the dashboard/activity list run - the same "good enough for this
// app's scale" pattern already used by loadTicketsList's Limit:100, just
// larger since this view intentionally spans every open ticket at once.
const managerFetchLimit = 5000

// queueStat is one row of the dashboard's per-queue open/breaching counts.
type queueStat struct {
	QueueID   int64
	Name      string
	Open      int
	Breaching int
}

// engineerLoad is one row of the dashboard's per-engineer load list.
type engineerLoad struct {
	UserID     int64
	Username   string
	Load       int
	Overloaded bool
}

// mttxSummary is the dashboard's aggregate MTTx tile (DESIGN/08 §8.6),
// computed directly from ticket stage timestamps rather than scraping this
// process's own /metrics endpoint.
type mttxSummary struct {
	MTTD, MTTA, MTTM, MTTR string // humanDuration-formatted, "-" if no samples
}

// mttxTrendDays is the sparkline window (RELEASE/v_3.0.0.md) - "last N
// shifts" from the original deferred item, read as calendar days since
// there's no shift/roster concept anywhere else in this codebase.
const mttxTrendDays = 14

type managerDashboardData struct {
	baseData
	QueueStats    []queueStat
	EngineerLoads []engineerLoad
	MTTx          mttxSummary
	MTTxTrendDays int
	// MTTxTrend* are precomputed inline-SVG sparklines (12-point stat-tile
	// convention) for self-hosted deployments without external Grafana/
	// Prometheus - see computeMTTxTrend/sparklineSVG.
	MTTDTrend, MTTATrend, MTTMTrend, MTTRTrend template.HTML
}

func (s *Server) handleManagerDashboard(w http.ResponseWriter, r *http.Request) {
	tickets, err := s.ticketSvc.List(repo.ListFilter{Limit: managerFetchLimit})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	queues, err := s.queues.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	users, err := s.users.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	now := time.Now()
	resolved, err := s.tickets.ListResolvedBetween(now.AddDate(0, 0, -mttxTrendDays), now.AddDate(0, 0, 1))
	if err != nil {
		s.log.Warn("manager dashboard: could not load MTTx trend data", "err", err)
	}
	trend := computeMTTxTrend(resolved, mttxTrendDays, now)

	s.render.Render(w, "manager_dashboard", managerDashboardData{
		baseData:      s.base(r, "Manager"),
		QueueStats:    queueStats(tickets, queues),
		EngineerLoads: engineerLoads(tickets, users),
		MTTx:          computeMTTx(tickets),
		MTTxTrendDays: mttxTrendDays,
		MTTDTrend:     sparklineSVG(mttxSeries(trend, func(p mttxTrendPoint) float64 { return p.MTTDMin }), "var(--tw-teal-600)"),
		MTTATrend:     sparklineSVG(mttxSeries(trend, func(p mttxTrendPoint) float64 { return p.MTTAMin }), "var(--tw-sage-500)"),
		MTTMTrend:     sparklineSVG(mttxSeries(trend, func(p mttxTrendPoint) float64 { return p.MTTMMin }), "var(--tw-amber-500)"),
		MTTRTrend:     sparklineSVG(mttxSeries(trend, func(p mttxTrendPoint) float64 { return p.MTTRMin }), "var(--tw-purple-500)"),
	})
}

func mttxSeries(trend []mttxTrendPoint, pick func(mttxTrendPoint) float64) []float64 {
	out := make([]float64, len(trend))
	for i, p := range trend {
		out[i] = pick(p)
	}
	return out
}

// queueStats aggregates open/breaching counts per queue. "Open" is anything
// short of Closed (Rejected never rests as Status - see stagebar.go - so it's
// already excluded); "breaching" additionally requires a past-due SLADueAt.
func queueStats(tickets []models.Ticket, queues []models.Queue) []queueStat {
	now := time.Now()
	stats := make(map[int64]*queueStat, len(queues))
	order := make([]int64, 0, len(queues))
	for _, q := range queues {
		stats[q.ID] = &queueStat{QueueID: q.ID, Name: q.Name}
		order = append(order, q.ID)
	}
	for _, t := range tickets {
		st, ok := stats[t.QueueID]
		if !ok || t.Status == models.StatusClosed {
			continue
		}
		st.Open++
		if t.SLADueAt != nil && now.After(*t.SLADueAt) {
			st.Breaching++
		}
	}
	result := make([]queueStat, 0, len(order))
	for _, id := range order {
		result = append(result, *stats[id])
	}
	return result
}

// engineerLoads counts each staff member's active (New/In Progress) assigned
// tickets and flags anyone above team-average + 1 - the simpler of the two
// thresholds DESIGN/08 §8.6 suggests (team-avg + 1 SD, or configurable);
// average+1 needs no extra config key and is legible at a glance.
func engineerLoads(tickets []models.Ticket, users []models.User) []engineerLoad {
	var staff []models.User
	for _, u := range users {
		if u.Role.IsAgent() {
			staff = append(staff, u)
		}
	}
	load := make(map[int64]int, len(staff))
	for _, t := range tickets {
		if t.AssigneeID == nil {
			continue
		}
		if t.Status != models.StatusNew && t.Status != models.StatusInProgress {
			continue
		}
		load[*t.AssigneeID]++
	}
	if len(staff) == 0 {
		return nil
	}
	total := 0
	for _, u := range staff {
		total += load[u.ID]
	}
	threshold := float64(total)/float64(len(staff)) + 1

	result := make([]engineerLoad, 0, len(staff))
	for _, u := range staff {
		l := load[u.ID]
		result = append(result, engineerLoad{UserID: u.ID, Username: u.Username, Load: l, Overloaded: float64(l) > threshold})
	}
	return result
}

// ticketMTTx returns one ticket's per-stage deltas, nil for any stage not
// yet reached - shared by computeMTTx (current aggregate) and
// computeMTTxTrend (daily buckets, RELEASE/v_3.0.0.md), so the two never
// drift apart on what counts as each metric.
func ticketMTTx(t models.Ticket) (mttd, mtta, mttm, mttr *time.Duration) {
	if t.DetectedAt != nil {
		d := t.DetectedAt.Sub(t.CreatedAt)
		mttd = &d
	}
	if t.AckedAt != nil && t.DetectedAt != nil {
		d := t.AckedAt.Sub(*t.DetectedAt)
		mtta = &d
	}
	if t.MitigatedAt != nil && t.AckedAt != nil {
		d := t.MitigatedAt.Sub(*t.AckedAt)
		mttm = &d
	}
	if t.ResolvedAt != nil {
		switch {
		case t.MitigatedAt != nil:
			d := t.ResolvedAt.Sub(*t.MitigatedAt)
			mttr = &d
		case t.AckedAt != nil:
			d := t.ResolvedAt.Sub(*t.AckedAt)
			mttr = &d
		}
	}
	return
}

func computeMTTx(tickets []models.Ticket) mttxSummary {
	var mttd, mtta, mttm, mttr durationAvg
	for _, t := range tickets {
		d, a, m, r := ticketMTTx(t)
		if d != nil {
			mttd.add(*d)
		}
		if a != nil {
			mtta.add(*a)
		}
		if m != nil {
			mttm.add(*m)
		}
		if r != nil {
			mttr.add(*r)
		}
	}
	return mttxSummary{MTTD: mttd.label(), MTTA: mtta.label(), MTTM: mttm.label(), MTTR: mttr.label()}
}

// mttxTrendPoint is one day's bucketed MTTx averages, in minutes (for
// sparkline plotting) - a day with no ticket resolved gets 0 rather than
// being dropped, so every sparkline stays evenly spaced across the window.
type mttxTrendPoint struct {
	Date                               string
	MTTDMin, MTTAMin, MTTMMin, MTTRMin float64
}

// computeMTTxTrend buckets tickets (the caller scopes these to the window via
// TicketRepo.ListResolvedBetween) by ResolvedAt's UTC calendar date into
// `days` consecutive points ending "now," oldest first - reuses ticketMTTx's
// exact per-ticket delta logic, just grouped by day instead of one running mean.
func computeMTTxTrend(tickets []models.Ticket, days int, now time.Time) []mttxTrendPoint {
	type bucket struct{ d, a, m, r durationAvg }
	buckets := make(map[string]*bucket, days)
	dates := make([]string, days)
	for i := 0; i < days; i++ {
		day := now.AddDate(0, 0, -(days - 1 - i)).UTC().Format("2006-01-02")
		dates[i] = day
		buckets[day] = &bucket{}
	}
	for _, t := range tickets {
		if t.ResolvedAt == nil {
			continue
		}
		b, ok := buckets[t.ResolvedAt.UTC().Format("2006-01-02")]
		if !ok {
			continue // outside the window - defensive, caller already scoped the query
		}
		d, a, m, r := ticketMTTx(t)
		if d != nil {
			b.d.add(*d)
		}
		if a != nil {
			b.a.add(*a)
		}
		if m != nil {
			b.m.add(*m)
		}
		if r != nil {
			b.r.add(*r)
		}
	}
	points := make([]mttxTrendPoint, days)
	for i, day := range dates {
		b := buckets[day]
		points[i] = mttxTrendPoint{Date: day, MTTDMin: b.d.minutes(), MTTAMin: b.a.minutes(), MTTMMin: b.m.minutes(), MTTRMin: b.r.minutes()}
	}
	return points
}

// durationAvg accumulates a running mean without keeping every sample around.
type durationAvg struct {
	sum   time.Duration
	count int
}

func (d *durationAvg) add(v time.Duration) {
	if v < 0 {
		return
	}
	d.sum += v
	d.count++
}

func (d *durationAvg) label() string {
	if d.count == 0 {
		return "-"
	}
	return humanDuration(time.Duration(math.Round(float64(d.sum) / float64(d.count))))
}

// minutes is the mean in minutes (0 for an empty bucket), for sparkline
// plotting where every day needs a plottable value, not a "-" placeholder.
func (d *durationAvg) minutes() float64 {
	if d.count == 0 {
		return 0
	}
	return (float64(d.sum) / float64(d.count)) / float64(time.Minute)
}

// sparklineSVG renders a 12-point stat-tile trend line (dataviz skill's
// "Figures" mark spec: a 2px de-emphasis-hue line, current period in the
// series' accent) as a small inline SVG - no chart library, no build step,
// consistent with this app's vendored-JS-only frontend. All-zero data (no
// tickets resolved in the window yet) renders a flat baseline rather than
// nothing, so the tile never looks broken.
func sparklineSVG(values []float64, accent string) template.HTML {
	const w, h, pad = 120.0, 28.0, 3.0
	if len(values) == 0 {
		return ""
	}
	maxVal := 0.0
	for _, v := range values {
		if v > maxVal {
			maxVal = v
		}
	}
	denom := len(values) - 1
	if denom < 1 {
		denom = 1
	}
	points := make([]string, len(values))
	for i, v := range values {
		x := pad + (w-2*pad)*float64(i)/float64(denom)
		y := h - pad
		if maxVal > 0 {
			y = pad + (h-2*pad)*(1-v/maxVal)
		}
		points[i] = fmt.Sprintf("%.1f,%.1f", x, y)
	}
	last := points[len(points)-1]
	lastXY := strings.SplitN(last, ",", 2)

	// nosemgrep: go.lang.security.audit.net.formatted-template-string.formatted-template-string -- every arg is a constant or a computed float (w, h, len(values), points, accent are all internal), no user input reaches this template
	svg := fmt.Sprintf(
		`<svg width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f" role="img" aria-label="trend, last %d days"><title>Last %d days</title>`+
			`<polyline points="%s" fill="none" stroke="var(--tw-border)" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/>`+
			`<circle cx="%s" cy="%s" r="3" fill="%s"/></svg>`,
		w, h, w, h, len(values), len(values), strings.Join(points, " "), lastXY[0], lastXY[1], accent,
	)
	return template.HTML(svg)
}

// --- Activity list --------------------------------------------------------

type activityRow struct {
	Ticket      models.Ticket
	QueueName   string
	AssigneeStr string
	LastMessage string
	LastAuthor  string
	LastAt      *time.Time
}

type managerActivityData struct {
	baseData
	Rows   []activityRow
	Agents []models.User
}

// handleManagerActivity is the "what's the latest on everything" scan view
// (DESIGN/08 §8.6): every open ticket, most-recently-updated first (the
// existing repo.List ordering already does this - TicketRepo.TouchUpdatedAt
// keeps note-only activity counted too), each row showing only the latest
// message across notes and status changes, not the full thread.
func (s *Server) handleManagerActivity(w http.ResponseWriter, r *http.Request) {
	tickets, err := s.ticketSvc.List(repo.ListFilter{
		Status: []string{string(models.StatusNew), string(models.StatusInProgress), string(models.StatusResolved)},
		Limit:  managerFetchLimit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ids := make([]int64, len(tickets))
	for i, t := range tickets {
		ids[i] = t.ID
	}
	latestNotes, err := s.noteSvc.LatestForTickets(ids)
	if err != nil {
		s.log.Warn("manager activity: load latest notes failed", "err", err)
	}

	queues, _ := s.queues.List()
	queueNames := make(map[int64]string, len(queues))
	for _, q := range queues {
		queueNames[q.ID] = q.Name
	}
	users, _ := s.users.List()
	userNames := make(map[int64]string, len(users))
	var agents []models.User
	for _, u := range users {
		userNames[u.ID] = u.Username
		if u.Role.IsAgent() {
			agents = append(agents, u)
		}
	}

	rows := make([]activityRow, 0, len(tickets))
	for _, t := range tickets {
		row := activityRow{Ticket: t, QueueName: queueNames[t.QueueID], AssigneeStr: "Unassigned"}
		if t.AssigneeID != nil {
			row.AssigneeStr = userNames[*t.AssigneeID]
		}
		if n, ok := latestNotes[t.ID]; ok {
			row.LastMessage = n.Body
			row.LastAuthor = userNames[n.AuthorID]
			createdAt := n.CreatedAt
			row.LastAt = &createdAt
		}
		rows = append(rows, row)
	}

	s.render.Render(w, "manager_activity", managerActivityData{
		baseData: s.base(r, "Manager"), Rows: rows, Agents: agents,
	})
}
