package service

import (
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// ProblemService implements Problem Management & RCA (DESIGN.md 3.4): grouping
// recurring tickets under a Problem record with a mandatory RCA report.
type ProblemService struct {
	problems *repo.ProblemRepo
	tags     *repo.TagRepo
}

func NewProblemService(problems *repo.ProblemRepo, tags *repo.TagRepo) *ProblemService {
	return &ProblemService{problems: problems, tags: tags}
}

func (s *ProblemService) Create(p *models.Problem) error        { return s.problems.Create(p) }
func (s *ProblemService) Update(p *models.Problem) error        { return s.problems.Update(p) }
func (s *ProblemService) Get(id int64) (*models.Problem, error) { return s.problems.Get(id) }
func (s *ProblemService) List() ([]models.Problem, error)       { return s.problems.List() }

// LinkTicket associates a ticket with a Problem and optionally tags it with an RCA label.
func (s *ProblemService) LinkTicket(problemID, ticketID int64, rcaLabel string) error {
	if err := s.problems.LinkTicket(problemID, ticketID); err != nil {
		return err
	}
	if rcaLabel == "" {
		return nil
	}
	tag, err := s.tags.GetOrCreate(rcaLabel, "rca")
	if err != nil {
		return err
	}
	return s.tags.AttachToTicket(ticketID, tag.ID)
}

func (s *ProblemService) UnlinkTicket(problemID, ticketID int64) error {
	return s.problems.UnlinkTicket(problemID, ticketID)
}

func (s *ProblemService) TicketsForProblem(problemID int64) ([]models.Ticket, error) {
	return s.problems.TicketsForProblem(problemID)
}
