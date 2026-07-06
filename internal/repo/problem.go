package repo

import (
	"gorm.io/gorm"

	"servicedesk/internal/models"
)

type ProblemRepo struct{ db *gorm.DB }

func NewProblemRepo(db *gorm.DB) *ProblemRepo { return &ProblemRepo{db: db} }

func (r *ProblemRepo) Create(p *models.Problem) error { return r.db.Create(p).Error }
func (r *ProblemRepo) Update(p *models.Problem) error { return r.db.Save(p).Error }

func (r *ProblemRepo) Get(id int64) (*models.Problem, error) {
	var p models.Problem
	if err := r.db.First(&p, id).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *ProblemRepo) List() ([]models.Problem, error) {
	var ps []models.Problem
	err := r.db.Order("created_at DESC").Find(&ps).Error
	return ps, err
}

func (r *ProblemRepo) LinkTicket(problemID, ticketID int64) error {
	m := models.ProblemTicket{ProblemID: problemID, TicketID: ticketID}
	return r.db.Where(m).FirstOrCreate(&m).Error
}

func (r *ProblemRepo) UnlinkTicket(problemID, ticketID int64) error {
	return r.db.Where("problem_id = ? AND ticket_id = ?", problemID, ticketID).Delete(&models.ProblemTicket{}).Error
}

func (r *ProblemRepo) TicketsForProblem(problemID int64) ([]models.Ticket, error) {
	var ts []models.Ticket
	err := r.db.Joins("JOIN problem_tickets ON problem_tickets.ticket_id = tickets.id").
		Where("problem_tickets.problem_id = ?", problemID).
		Order("tickets.created_at DESC").Find(&ts).Error
	return ts, err
}
