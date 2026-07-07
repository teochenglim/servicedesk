package service

import (
	"context"
	"fmt"
	"strings"

	"servicedesk/internal/llm"
	"servicedesk/internal/repo"
)

// Default*Prompt are the system prompts for the stateless "AI draft" button
// (DESIGN/08 §8.8) - each overridable via the matching Config.AIDraft*Prompt.
// Unlike AISummaryService, drafting is one-shot and never persisted: the
// result lands in an editable field, the human reviews and posts it.
const (
	DefaultDraftDescriptionPrompt = `You help a customer turn a rough, informal note into a clear IT support ticket description.
Rewrite it to cover: what's failing, when it started, and how often it happens - using only information
implied by the rough note, never inventing details. Respond with ONLY the rewritten description text, no preamble.`

	DefaultDraftResolutionPrompt = `You are an IT support engineer closing out a ticket. Given the ticket's title, description,
and note history below, write a concise resolution summary suitable for the customer: what the problem was and how it was fixed.
Respond with ONLY the resolution text, no preamble.`

	DefaultDraftTransferPrompt = `You are an IT support engineer handing a ticket off to a colleague. Given the ticket's title,
description, and note history below, write a short transfer note: current state, what's been tried, what's still needed.
Respond with ONLY the transfer note text, no preamble.`
)

// AIDraftService is stateless one-shot drafting (DESIGN/08 §8.8) - no
// snapshot persistence, unlike AISummaryService; the draft always lands in
// an editable field for the human to review before posting.
type AIDraftService struct {
	tickets           *repo.TicketRepo
	notes             *repo.NoteRepo
	llm               llm.Client
	descriptionPrompt string
	resolutionPrompt  string
	transferPrompt    string
}

func NewAIDraftService(tickets *repo.TicketRepo, notes *repo.NoteRepo, client llm.Client, descriptionPrompt, resolutionPrompt, transferPrompt string) *AIDraftService {
	if descriptionPrompt == "" {
		descriptionPrompt = DefaultDraftDescriptionPrompt
	}
	if resolutionPrompt == "" {
		resolutionPrompt = DefaultDraftResolutionPrompt
	}
	if transferPrompt == "" {
		transferPrompt = DefaultDraftTransferPrompt
	}
	return &AIDraftService{
		tickets: tickets, notes: notes, llm: client,
		descriptionPrompt: descriptionPrompt, resolutionPrompt: resolutionPrompt, transferPrompt: transferPrompt,
	}
}

// DraftDescription drafts a clearer ticket description from a rough first
// attempt - used on ticket submission, before any Ticket row exists yet.
func (s *AIDraftService) DraftDescription(rough string) (string, error) {
	return s.llm.Complete(context.Background(), []llm.Message{
		{Role: "system", Content: s.descriptionPrompt},
		{Role: "user", Content: rough},
	})
}

// DraftResolutionNote drafts a resolution summary from an existing ticket's
// accumulated notes.
func (s *AIDraftService) DraftResolutionNote(ticketID int64) (string, error) {
	return s.draftFromHistory(ticketID, s.resolutionPrompt)
}

// DraftTransferReason drafts a handoff note from an existing ticket's
// accumulated notes.
func (s *AIDraftService) DraftTransferReason(ticketID int64) (string, error) {
	return s.draftFromHistory(ticketID, s.transferPrompt)
}

func (s *AIDraftService) draftFromHistory(ticketID int64, prompt string) (string, error) {
	t, err := s.tickets.Get(ticketID)
	if err != nil {
		return "", err
	}
	notes, err := s.notes.ListByTicket(ticketID, true)
	if err != nil {
		return "", err
	}
	var history strings.Builder
	fmt.Fprintf(&history, "Title: %s\nDescription: %s\n\nNotes:\n", t.Title, t.Description)
	for _, n := range notes {
		fmt.Fprintf(&history, "- %s\n", n.Body)
	}
	return s.llm.Complete(context.Background(), []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: history.String()},
	})
}
