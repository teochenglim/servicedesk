package service

import (
	"encoding/json"
	"testing"

	"servicedesk/internal/auth"
	"servicedesk/internal/db"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

func newTestKBService(t *testing.T) (*KBService, *repo.TicketRepo, *repo.AISnapshotRepo, *repo.ServiceRepo) {
	t.Helper()
	gdb, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := gdb.DB()
		sqlDB.Close()
	})
	tickets := repo.NewTicketRepo(gdb)
	snapshots := repo.NewAISnapshotRepo(gdb)
	articles := repo.NewKBArticleRepo(gdb)
	services := repo.NewServiceRepo(gdb)
	return NewKBService(articles, tickets, snapshots), tickets, snapshots, services
}

func TestKB_ProposeFromTicketSeedsFieldsFromSnapshot(t *testing.T) {
	svc, tickets, snapshots, _ := newTestKBService(t)
	ticketID := mustCreateTicket(t, tickets)

	fields, _ := json.Marshal(SummaryFields{
		"symptom": "VPN drops every 10 minutes", "what_tried": "reconnected manually",
		"diagnosis": "idle-session timeout too low", "resolution": "raised timeout to 60 minutes",
	})
	if err := snapshots.Create(&models.TicketAISnapshot{TicketID: ticketID, Source: "ai", Fields: string(fields), EditedFields: "[]"}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	a, err := svc.ProposeFromTicket(ticketID)
	if err != nil {
		t.Fatalf("ProposeFromTicket: %v", err)
	}
	if a.Status != models.KBStatusDraft {
		t.Errorf("Status = %q, want draft", a.Status)
	}
	if a.Symptom != "VPN drops every 10 minutes" {
		t.Errorf("Symptom = %q", a.Symptom)
	}
	if a.SelfServiceSteps != "reconnected manually" {
		t.Errorf("SelfServiceSteps = %q", a.SelfServiceSteps)
	}
	if a.RootCause != "idle-session timeout too low" {
		t.Errorf("RootCause = %q", a.RootCause)
	}
	if a.Resolution != "raised timeout to 60 minutes" {
		t.Errorf("Resolution = %q", a.Resolution)
	}
	if a.SourceTicketID == nil || *a.SourceTicketID != ticketID {
		t.Errorf("SourceTicketID = %v, want %d", a.SourceTicketID, ticketID)
	}
}

// TestKB_ProposeFromTicketWithoutSnapshot covers AI-disabled deployments
// (RELEASE/v_2.1.0.md): no TicketAISnapshot exists yet, so the draft is still
// created with empty seed fields rather than erroring.
func TestKB_ProposeFromTicketWithoutSnapshot(t *testing.T) {
	svc, tickets, _, _ := newTestKBService(t)
	ticketID := mustCreateTicket(t, tickets)

	a, err := svc.ProposeFromTicket(ticketID)
	if err != nil {
		t.Fatalf("ProposeFromTicket: %v", err)
	}
	if a.Status != models.KBStatusDraft {
		t.Errorf("Status = %q, want draft", a.Status)
	}
	if a.Symptom != "" {
		t.Errorf("Symptom = %q, want empty seed", a.Symptom)
	}
}

// TestKB_ProposeFromTicketTwiceCreatesTwoDrafts is the KBService half of
// "fires exactly once per resolution, including again after reopen -> re-resolve"
// (RELEASE/v_2.1.0.md's checklist) - each call to ProposeFromTicket must be
// independent, not silently overwrite the prior draft.
func TestKB_ProposeFromTicketTwiceCreatesTwoDrafts(t *testing.T) {
	svc, tickets, _, _ := newTestKBService(t)
	ticketID := mustCreateTicket(t, tickets)

	first, err := svc.ProposeFromTicket(ticketID)
	if err != nil {
		t.Fatalf("first ProposeFromTicket: %v", err)
	}
	second, err := svc.ProposeFromTicket(ticketID)
	if err != nil {
		t.Fatalf("second ProposeFromTicket: %v", err)
	}
	if first.ID == second.ID {
		t.Fatal("expected two independent draft rows, got the same ID")
	}
	drafts, err := svc.ListDrafts()
	if err != nil {
		t.Fatalf("ListDrafts: %v", err)
	}
	if len(drafts) != 2 {
		t.Errorf("drafts = %d, want 2", len(drafts))
	}
}

func TestKB_ProposeFromTicketLinksService(t *testing.T) {
	svc, tickets, _, services := newTestKBService(t)
	mailService := &models.Service{Name: "Mail Service", Criticality: models.ServiceCriticalityCritical}
	if err := services.Create(mailService); err != nil {
		t.Fatalf("create service: %v", err)
	}

	tk := &models.Ticket{Title: "Mail down", QueueID: 1, Status: models.StatusResolved, ServiceID: &mailService.ID}
	if err := tickets.Create(tk); err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	a, err := svc.ProposeFromTicket(tk.ID)
	if err != nil {
		t.Fatalf("ProposeFromTicket: %v", err)
	}
	linked, err := svc.ServicesForArticle(a.ID)
	if err != nil {
		t.Fatalf("ServicesForArticle: %v", err)
	}
	if len(linked) != 1 || linked[0].ID != mailService.ID {
		t.Errorf("linked services = %+v, want [{ID: %d}]", linked, mailService.ID)
	}
}

func TestKB_MatchForSymptom(t *testing.T) {
	svc, tickets, _, _ := newTestKBService(t)
	ticketID := mustCreateTicket(t, tickets)

	a, err := svc.ProposeFromTicket(ticketID)
	if err != nil {
		t.Fatalf("ProposeFromTicket: %v", err)
	}
	if _, err := svc.Update(a.ID, KBArticleUpdate{
		Title: "VPN drops", Symptom: "vpn connection drops every ten minutes", WhatToObserve: "session disconnects repeatedly",
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if _, err := svc.Approve(1, a.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	match, score, err := svc.MatchForSymptom("vpn connection drops every few minutes", "")
	if err != nil {
		t.Fatalf("MatchForSymptom: %v", err)
	}
	if match == nil || match.ID != a.ID {
		t.Fatalf("match = %+v, want article %d", match, a.ID)
	}
	if score <= 0 {
		t.Errorf("score = %v, want > 0", score)
	}

	noMatch, _, err := svc.MatchForSymptom("completely unrelated printer jam issue", "")
	if err != nil {
		t.Fatalf("MatchForSymptom (no match): %v", err)
	}
	if noMatch != nil {
		t.Errorf("expected no match for unrelated symptom, got %+v", noMatch)
	}
}

func TestKB_ApproveRejectsNonDraft(t *testing.T) {
	svc, tickets, _, _ := newTestKBService(t)
	ticketID := mustCreateTicket(t, tickets)
	a, err := svc.ProposeFromTicket(ticketID)
	if err != nil {
		t.Fatalf("ProposeFromTicket: %v", err)
	}
	if _, err := svc.Approve(1, a.ID); err != nil {
		t.Fatalf("first Approve: %v", err)
	}
	if _, err := svc.Approve(1, a.ID); err != ErrKBNotDraft {
		t.Errorf("second Approve err = %v, want ErrKBNotDraft", err)
	}
}

func TestKB_CanView(t *testing.T) {
	svc, tickets, _, _ := newTestKBService(t)
	ticketID := mustCreateTicket(t, tickets)
	draft, err := svc.ProposeFromTicket(ticketID)
	if err != nil {
		t.Fatalf("ProposeFromTicket: %v", err)
	}

	customer := &auth.Claims{Role: models.RoleCustomer}
	engineer := &auth.Claims{Role: models.RoleEngineer}

	if svc.CanView(customer, draft) {
		t.Error("a Customer must not be able to view an unapproved draft")
	}
	if !svc.CanView(engineer, draft) {
		t.Error("staff must be able to view a draft")
	}

	published, err := svc.Approve(1, draft.ID)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !svc.CanView(customer, published) {
		t.Error("a Customer must be able to view a published article")
	}
}
