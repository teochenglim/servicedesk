package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"servicedesk/internal/llm"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// summaryFieldOrder is the AI Ticket Intelligence Panel's field set
// (DESIGN/08 §8.9) - deliberately Fields as a map[string]string rather than
// a fixed struct, so merge-with-locked-fields (below) is a plain map copy,
// and the panel's Suggest-Split/agenda-items checklist can be added later as
// just another key without a Go type change. Key order here is display order.
var summaryFieldOrder = []struct{ Key, Label string }{
	{"symptom", "Symptom"},
	{"what_tried", "What the user already tried"},
	{"problem_statement", "Problem statement"},
	{"diagnosis", "Diagnosis"},
	{"mitigation", "Mitigation"},
	{"resolution", "Resolution"},
}

// DefaultSummaryPrompt instructs the model to extract the panel's fields as
// strict JSON from the ticket history that follows in the user message.
// Overridable via Config.AISummaryPrompt for wording/tone/model-specific tuning.
const DefaultSummaryPrompt = `You are an assistant summarizing an IT support ticket for the engineer working it.
Given the ticket's title, description, and full note history, extract a structured summary.
Respond with ONLY a JSON object (no markdown fences, no commentary) with exactly these keys,
each a short plain-text value (use "" if there's not enough information yet):
{"symptom": "...", "what_tried": "...", "problem_statement": "...", "diagnosis": "...", "mitigation": "...", "resolution": "..."}`

// SummaryFields is the panel's field set as a plain map, keyed by
// summaryFieldOrder's Key values.
type SummaryFields map[string]string

// SummarySnapshotView is the template/handler-facing shape of a snapshot:
// fields in display order, each annotated with whether it's locked (edited).
type SummarySnapshotView struct {
	ID          int64
	GeneratedAt time.Time
	Source      string
	Fields      []SummaryFieldView
}

type SummaryFieldView struct {
	Key, Label, Value string
	Locked            bool
}

// AISummaryService implements the AI Ticket Intelligence Panel (DESIGN/08
// §8.9): regenerated wholesale on every new note, human edits lock a field
// against being silently overwritten by the next regeneration.
type AISummaryService struct {
	snapshots *repo.AISnapshotRepo
	tickets   *repo.TicketRepo
	notes     *repo.NoteRepo
	llm       llm.Client
	prompt    string
}

func NewAISummaryService(snapshots *repo.AISnapshotRepo, tickets *repo.TicketRepo, notes *repo.NoteRepo, client llm.Client, prompt string) *AISummaryService {
	if prompt == "" {
		prompt = DefaultSummaryPrompt
	}
	return &AISummaryService{snapshots: snapshots, tickets: tickets, notes: notes, llm: client, prompt: prompt}
}

// Regenerate builds a fresh AI-sourced snapshot from the ticket's full
// history (internal notes included - this panel is Engineer-facing),
// preserving any human-edited (locked) fields from the latest snapshot
// rather than overwriting them (DESIGN/08 §8.9).
func (s *AISummaryService) Regenerate(ticketID int64, triggeringNoteID *int64) error {
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return err
	}
	notes, err := s.notes.ListByTicket(ticketID, true)
	if err != nil {
		return err
	}

	fresh, err := s.complete(t, notes)
	if err != nil {
		return err
	}

	locked := []string{}
	if prev, perr := s.snapshots.Latest(ticketID); perr == nil {
		var prevFields SummaryFields
		_ = json.Unmarshal([]byte(prev.Fields), &prevFields)
		_ = json.Unmarshal([]byte(prev.EditedFields), &locked)
		for _, k := range locked {
			if v, ok := prevFields[k]; ok {
				fresh[k] = v
			}
		}
	}

	return s.persist(ticketID, triggeringNoteID, "ai", fresh, locked)
}

// EditField records a human correction as its own snapshot and locks that
// field against future auto-regeneration (DESIGN/08 §8.9) - a corrected
// extraction is closer to a gold label than a raw model guess.
func (s *AISummaryService) EditField(ticketID int64, field, value string) error {
	if !validSummaryField(field) {
		return fmt.Errorf("unknown summary field %q", field)
	}
	fields, locked, err := s.latestOrEmpty(ticketID)
	if err != nil {
		return err
	}
	fields[field] = value
	if !contains(locked, field) {
		locked = append(locked, field)
	}
	return s.persist(ticketID, nil, "human_edit", fields, locked)
}

// RegenerateField re-asks the model for the whole panel but only takes the
// requested field from the fresh result, then unlocks it - "let the AI take
// another pass" on a field a human previously corrected (DESIGN/08 §8.9).
func (s *AISummaryService) RegenerateField(ticketID int64, field string) error {
	if !validSummaryField(field) {
		return fmt.Errorf("unknown summary field %q", field)
	}
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return err
	}
	notes, err := s.notes.ListByTicket(ticketID, true)
	if err != nil {
		return err
	}
	fresh, err := s.complete(t, notes)
	if err != nil {
		return err
	}

	fields, locked, err := s.latestOrEmpty(ticketID)
	if err != nil {
		return err
	}
	fields[field] = fresh[field]
	locked = removeString(locked, field)
	return s.persist(ticketID, nil, "ai", fields, locked)
}

// Latest returns the current panel state for rendering, or nil if the panel
// has never been generated for this ticket (gorm.ErrRecordNotFound).
func (s *AISummaryService) Latest(ticketID int64) (*SummarySnapshotView, error) {
	snap, err := s.snapshots.Latest(ticketID)
	if err != nil {
		return nil, err
	}
	var fields SummaryFields
	_ = json.Unmarshal([]byte(snap.Fields), &fields)
	var locked []string
	_ = json.Unmarshal([]byte(snap.EditedFields), &locked)

	view := &SummarySnapshotView{ID: snap.ID, GeneratedAt: snap.GeneratedAt, Source: snap.Source}
	for _, f := range summaryFieldOrder {
		view.Fields = append(view.Fields, SummaryFieldView{
			Key: f.Key, Label: f.Label, Value: fields[f.Key], Locked: contains(locked, f.Key),
		})
	}
	return view, nil
}

func (s *AISummaryService) latestOrEmpty(ticketID int64) (SummaryFields, []string, error) {
	prev, err := s.snapshots.Latest(ticketID)
	if err != nil {
		return SummaryFields{}, nil, nil // no prior snapshot - empty base is fine
	}
	var fields SummaryFields
	_ = json.Unmarshal([]byte(prev.Fields), &fields)
	if fields == nil {
		fields = SummaryFields{}
	}
	var locked []string
	_ = json.Unmarshal([]byte(prev.EditedFields), &locked)
	return fields, locked, nil
}

func (s *AISummaryService) persist(ticketID int64, triggeringNoteID *int64, source string, fields SummaryFields, locked []string) error {
	fieldsJSON, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	lockedJSON, err := json.Marshal(locked)
	if err != nil {
		return err
	}
	return s.snapshots.Create(&models.TicketAISnapshot{
		TicketID: ticketID, TriggeringNoteID: triggeringNoteID, Source: source,
		Fields: string(fieldsJSON), EditedFields: string(lockedJSON),
	})
}

// complete builds the ticket-history prompt and parses the model's JSON reply.
func (s *AISummaryService) complete(t *models.Ticket, notes []models.Note) (SummaryFields, error) {
	var history strings.Builder
	fmt.Fprintf(&history, "Title: %s\nDescription: %s\n\nNotes:\n", t.Title, t.Description)
	for _, n := range notes {
		kind := "external"
		if n.Internal {
			kind = "internal"
		}
		fmt.Fprintf(&history, "- [%s] %s\n", kind, n.Body)
	}

	reply, err := s.llm.Complete(context.Background(), []llm.Message{
		{Role: "system", Content: s.prompt},
		{Role: "user", Content: history.String()},
	})
	if err != nil {
		return nil, err
	}

	var fields SummaryFields
	if err := json.Unmarshal([]byte(extractJSONObject(reply)), &fields); err != nil {
		return nil, fmt.Errorf("aisummary: model reply was not valid JSON: %w", err)
	}
	if fields == nil {
		fields = SummaryFields{}
	}
	return fields, nil
}

// extractJSONObject trims anything before the first '{' and after the last
// '}' - small models frequently wrap JSON in markdown fences or a sentence
// of preamble despite instructions not to.
func extractJSONObject(s string) string {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}

func validSummaryField(field string) bool {
	for _, f := range summaryFieldOrder {
		if f.Key == field {
			return true
		}
	}
	return false
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func removeString(ss []string, v string) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if s != v {
			out = append(out, s)
		}
	}
	return out
}
