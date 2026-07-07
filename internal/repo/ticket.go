package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type TicketRepo struct{ db *gorm.DB }

func NewTicketRepo(db *gorm.DB) *TicketRepo { return &TicketRepo{db: db} }

func (r *TicketRepo) Create(t *models.Ticket) error {
	return r.db.Create(t).Error
}

func (r *TicketRepo) Get(id int64) (*models.Ticket, error) {
	var t models.Ticket
	if err := r.db.First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TicketRepo) Update(t *models.Ticket) error {
	return r.db.Save(t).Error
}

// ListFilter drives the advanced filters + saved views from the PRD (3.7).
type ListFilter struct {
	Status     []string
	Priority   []string
	QueueID    *int64
	QueueIDs   []int64 // e.g. "my queues" view: every queue an Engineer belongs to
	Category   string
	Label      string
	AssigneeID *int64
	CreatorID  *int64
	WatcherID  *int64 // "watched tickets" view
	Query      string // full text search over title/description/notes
	Limit      int
	Offset     int

	// CustomerScope enforces multi-tenant visibility for the Customer role:
	// only tickets in their org that they created or were added to watch.
	// Engineer/Manager/SystemAdmin never set this - they see every org.
	CustomerScope *CustomerScope
}

type CustomerScope struct {
	OrgID  int64
	UserID int64
}

func (r *TicketRepo) List(f ListFilter) ([]models.Ticket, error) {
	q := r.db.Model(&models.Ticket{}).Distinct("tickets.*")

	if len(f.Status) > 0 {
		q = q.Where("status IN ?", f.Status)
	}
	if len(f.Priority) > 0 {
		q = q.Where("priority IN ?", f.Priority)
	}
	if f.QueueID != nil {
		q = q.Where("queue_id = ?", *f.QueueID)
	}
	if len(f.QueueIDs) > 0 {
		q = q.Where("queue_id IN ?", f.QueueIDs)
	}
	if f.Category != "" {
		q = q.Where("category = ?", f.Category)
	}
	if f.AssigneeID != nil {
		q = q.Where("assignee_id = ?", *f.AssigneeID)
	}
	if f.CreatorID != nil {
		q = q.Where("creator_id = ?", *f.CreatorID)
	}
	if f.WatcherID != nil {
		q = q.Joins("JOIN watchers ON watchers.ticket_id = tickets.id AND watchers.user_id = ?", *f.WatcherID)
	}
	if f.Label != "" {
		q = q.Joins("JOIN ticket_tags ON ticket_tags.ticket_id = tickets.id").
			Joins("JOIN tags ON tags.id = ticket_tags.tag_id AND tags.name = ?", f.Label)
	}
	if f.Query != "" {
		q = q.Where(r.searchPredicate(), f.Query, f.Query)
	}
	if f.CustomerScope != nil {
		q = q.Where("tickets.org_id = ? AND (tickets.creator_id = ? OR tickets.id IN (SELECT ticket_id FROM watchers WHERE user_id = ?))",
			f.CustomerScope.OrgID, f.CustomerScope.UserID, f.CustomerScope.UserID)
	}
	if f.Limit > 0 {
		q = q.Limit(f.Limit).Offset(f.Offset)
	}

	var ts []models.Ticket
	err := q.Order("tickets.updated_at DESC").Find(&ts).Error
	return ts, err
}

// searchPredicate returns the dialect-appropriate full-text search clause
// (3.7: search across Title, Description, and Notes), taking two "?" args
// both times (matched twice against the same query string).
func (r *TicketRepo) searchPredicate() string {
	switch r.db.Dialector.Name() {
	case "mysql":
		return `(MATCH(tickets.title, tickets.description) AGAINST(? IN NATURAL LANGUAGE MODE)
			OR tickets.id IN (SELECT ticket_id FROM notes WHERE MATCH(notes.body) AGAINST(? IN NATURAL LANGUAGE MODE)))`
	case "postgres":
		return `(to_tsvector('english', tickets.title || ' ' || tickets.description) @@ plainto_tsquery('english', ?)
			OR tickets.id IN (SELECT ticket_id FROM notes WHERE to_tsvector('english', notes.body) @@ plainto_tsquery('english', ?)))`
	default: // sqlite: FTS5 virtual tables set up in db.applySQLiteFTS
		return `(tickets.id IN (SELECT rowid FROM tickets_fts WHERE tickets_fts MATCH ?)
			OR tickets.id IN (SELECT ticket_id FROM notes WHERE id IN (SELECT rowid FROM notes_fts WHERE notes_fts MATCH ?)))`
	}
}
