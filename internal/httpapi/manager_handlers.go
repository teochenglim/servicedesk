package httpapi

import (
	"math"
	"net/http"
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

type managerDashboardData struct {
	baseData
	QueueStats    []queueStat
	EngineerLoads []engineerLoad
	MTTx          mttxSummary
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

	s.render.Render(w, "manager_dashboard", managerDashboardData{
		baseData:      s.base(r, "Manager"),
		QueueStats:    queueStats(tickets, queues),
		EngineerLoads: engineerLoads(tickets, users),
		MTTx:          computeMTTx(tickets),
	})
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

func computeMTTx(tickets []models.Ticket) mttxSummary {
	var mttd, mtta, mttm, mttr durationAvg
	for _, t := range tickets {
		if t.DetectedAt != nil {
			mttd.add(t.DetectedAt.Sub(t.CreatedAt))
		}
		if t.AckedAt != nil && t.DetectedAt != nil {
			mtta.add(t.AckedAt.Sub(*t.DetectedAt))
		}
		if t.MitigatedAt != nil && t.AckedAt != nil {
			mttm.add(t.MitigatedAt.Sub(*t.AckedAt))
		}
		if t.ResolvedAt != nil {
			switch {
			case t.MitigatedAt != nil:
				mttr.add(t.ResolvedAt.Sub(*t.MitigatedAt))
			case t.AckedAt != nil:
				mttr.add(t.ResolvedAt.Sub(*t.AckedAt))
			}
		}
	}
	return mttxSummary{MTTD: mttd.label(), MTTA: mtta.label(), MTTM: mttm.label(), MTTR: mttr.label()}
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
