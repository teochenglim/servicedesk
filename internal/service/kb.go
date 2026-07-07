package service

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

var ErrKBNotDraft = errors.New("kb article is not a draft")

// matchThreshold is a deliberately simple token-overlap cutoff - "simplest
// first pass" per DESIGN/08 §8.10's own framing; smarter matching
// (embeddings, etc.) is a future enhancement, not a prerequisite.
const matchThreshold = 0.3

// KBService implements the Knowledge Base Feedback Loop (DESIGN/08 §8.10):
// every resolved ticket becomes a candidate contribution to a living,
// external-facing Knowledge Base, curated by a human before anything
// publishes. Named KBService (not KBArticleService) to avoid colliding with
// the models.KBArticleService join-table struct.
type KBService struct {
	articles  *repo.KBArticleRepo
	tickets   *repo.TicketRepo
	snapshots *repo.AISnapshotRepo
}

func NewKBService(articles *repo.KBArticleRepo, tickets *repo.TicketRepo, snapshots *repo.AISnapshotRepo) *KBService {
	return &KBService{articles: articles, tickets: tickets, snapshots: snapshots}
}

// ProposeFromTicket turns a resolved ticket into a draft KBArticle (DESIGN/08
// §8.10 step 1), called by TicketService.Transition exactly once per
// resolution via the KBProposalTrigger interface (interfaces.go). Seeds
// fields from the ticket's latest AI Ticket Intelligence Panel snapshot when
// one exists; falls back to empty seed fields otherwise (e.g. AI disabled),
// so the feature works independent of SERVICEDESK_AI_ENABLED - a human
// curator fills in the rest (validation steps, blast radius, etc.) before approving.
func (s *KBService) ProposeFromTicket(ticketID int64) (*models.KBArticle, error) {
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return nil, err
	}

	var symptom, whatTried, diagnosis, resolution string
	var snapshotID *int64
	if snap, err := s.snapshots.Latest(ticketID); err == nil {
		snapshotID = &snap.ID
		var fields SummaryFields
		_ = json.Unmarshal([]byte(snap.Fields), &fields)
		symptom, whatTried, diagnosis, resolution = fields["symptom"], fields["what_tried"], fields["diagnosis"], fields["resolution"]
	}

	authorID := t.CreatorID
	if t.AssigneeID != nil {
		authorID = *t.AssigneeID
	}

	a := &models.KBArticle{
		Title:            t.Title,
		Status:           models.KBStatusDraft,
		Symptom:          symptom,
		SelfServiceSteps: whatTried,
		Resolution:       resolution,
		RootCause:        diagnosis,
		ValidationSteps:  diagnosis,
		SourceTicketID:   &ticketID,
		SourceSnapshotID: snapshotID,
		CreatedByID:      authorID,
	}

	if match, score, err := s.MatchForSymptom(symptom, ""); err == nil && match != nil && score >= matchThreshold {
		a.RelatedArticleID = &match.ID
	}

	if err := s.articles.Create(a); err != nil {
		return nil, err
	}

	if t.ServiceID != nil {
		if err := s.articles.LinkService(a.ID, *t.ServiceID); err != nil {
			return nil, err
		}
	}

	return a, nil
}

// Propose implements KBProposalTrigger for TicketService - discards the
// created article; callers that need it call ProposeFromTicket directly.
func (s *KBService) Propose(ticketID int64) error {
	_, err := s.ProposeFromTicket(ticketID)
	return err
}

// MatchForSymptom scores published articles' Symptom+WhatToObserve against
// the given text via lowercased token-set Jaccard overlap - matching runs on
// the structured extraction, not raw ticket text (DESIGN/08 §8.10), and this
// is deliberately the simplest workable version of that. Returns the
// highest-scoring article and its score, or (nil, 0, nil) if nothing scores above 0.
func (s *KBService) MatchForSymptom(symptom, whatToObserve string) (*models.KBArticle, float64, error) {
	published, err := s.articles.ListPublished()
	if err != nil {
		return nil, 0, err
	}
	query := tokenSet(symptom + " " + whatToObserve)
	if len(query) == 0 {
		return nil, 0, nil
	}

	var best *models.KBArticle
	var bestScore float64
	for i := range published {
		cand := tokenSet(published[i].Symptom + " " + published[i].WhatToObserve)
		if score := jaccard(query, cand); score > bestScore {
			bestScore = score
			best = &published[i]
		}
	}
	return best, bestScore, nil
}

func tokenSet(s string) map[string]bool {
	tokens := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		if len(w) > 2 { // skip tiny stopword-ish tokens ("a", "is", ...)
			tokens[w] = true
		}
	}
	return tokens
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for w := range a {
		if b[w] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	return float64(intersection) / float64(union)
}

func (s *KBService) Get(id int64) (*models.KBArticle, error) { return s.articles.Get(id) }

func (s *KBService) ListPublished() ([]models.KBArticle, error) { return s.articles.ListPublished() }

func (s *KBService) ListDrafts() ([]models.KBArticle, error) {
	return s.articles.ListByStatus(models.KBStatusDraft)
}

func (s *KBService) ServicesForArticle(id int64) ([]models.Service, error) {
	return s.articles.ServicesForArticle(id)
}

// KBArticleUpdate is the curator-editable field set (RELEASE/v_2.1.0.md) -
// deliberately a separate struct from models.KBArticle so lifecycle/
// provenance fields (Status, ApprovedByID, PublishedAt, ...) can't be
// clobbered by a form post.
type KBArticleUpdate struct {
	Title, Symptom, WhatToObserve, SelfServiceSteps, Resolution string
	Environment, RootCause, ValidationSteps, ResolutionSteps    string
	Workaround, BlastRadius                                     string
	ServiceIDs                                                  []int64
}

func (s *KBService) Update(id int64, in KBArticleUpdate) (*models.KBArticle, error) {
	a, err := s.articles.Get(id)
	if err != nil {
		return nil, err
	}
	a.Title, a.Symptom, a.WhatToObserve = in.Title, in.Symptom, in.WhatToObserve
	a.SelfServiceSteps, a.Resolution = in.SelfServiceSteps, in.Resolution
	a.Environment, a.RootCause, a.ValidationSteps = in.Environment, in.RootCause, in.ValidationSteps
	a.ResolutionSteps, a.Workaround, a.BlastRadius = in.ResolutionSteps, in.Workaround, in.BlastRadius
	if err := s.articles.Update(a); err != nil {
		return nil, err
	}
	if in.ServiceIDs != nil {
		if err := s.setServices(a.ID, in.ServiceIDs); err != nil {
			return nil, err
		}
	}
	return a, nil
}

func (s *KBService) setServices(articleID int64, serviceIDs []int64) error {
	existing, err := s.articles.ServicesForArticle(articleID)
	if err != nil {
		return err
	}
	want := map[int64]bool{}
	for _, id := range serviceIDs {
		want[id] = true
	}
	have := map[int64]bool{}
	for _, svc := range existing {
		have[svc.ID] = true
		if !want[svc.ID] {
			if err := s.articles.UnlinkService(articleID, svc.ID); err != nil {
				return err
			}
		}
	}
	for id := range want {
		if !have[id] {
			if err := s.articles.LinkService(articleID, id); err != nil {
				return err
			}
		}
	}
	return nil
}

// Approve publishes a draft (DESIGN/08 §8.10 step 2's human curation gate) -
// the one true trust-boundary transition; nothing else moves an article's
// Status to published.
func (s *KBService) Approve(actorID, id int64) (*models.KBArticle, error) {
	a, err := s.articles.Get(id)
	if err != nil {
		return nil, err
	}
	if a.Status != models.KBStatusDraft {
		return nil, ErrKBNotDraft
	}
	now := time.Now()
	a.Status = models.KBStatusPublished
	a.ApprovedByID = &actorID
	a.PublishedAt = &now
	if err := s.articles.Update(a); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *KBService) Delete(id int64) error { return s.articles.Delete(id) }

// CanView is the one trust-boundary check for the Knowledge Base (DESIGN/08
// §8.10/§8.11): an unapproved draft must never reach a Customer-facing surface.
func (s *KBService) CanView(actor *auth.Claims, a *models.KBArticle) bool {
	return a.Status == models.KBStatusPublished || (actor != nil && actor.Role.IsAgent())
}
