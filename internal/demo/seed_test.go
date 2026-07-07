package demo

import (
	"testing"

	"gorm.io/gorm"

	"servicedesk/internal/db"
	"servicedesk/internal/logging"
	"servicedesk/internal/models"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	gdb, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := gdb.DB()
		sqlDB.Close()
	})
	return gdb
}

func count(t *testing.T, gdb *gorm.DB, model any) int64 {
	t.Helper()
	var n int64
	if err := gdb.Model(model).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestEmpty(t *testing.T) {
	gdb := testDB(t)
	empty, err := Empty(gdb)
	if err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if !empty {
		t.Fatal("expected a fresh DB to be empty")
	}

	if err := Seed(gdb, logging.New("error")); err != nil {
		t.Fatalf("Seed: %v", err)
	}
	empty, err = Empty(gdb)
	if err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if empty {
		t.Fatal("expected DB to be non-empty after Seed")
	}
}

func TestSeed_CreatesExpectedCounts(t *testing.T) {
	gdb := testDB(t)
	log := logging.New("error")
	if err := Seed(gdb, log); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	if got := count(t, gdb, &models.Organization{}); got != 3 {
		t.Errorf("organizations = %d, want 3", got)
	}
	queueNames := make([]string, len(queueSpecs))
	for i, q := range queueSpecs {
		queueNames[i] = q.Name
	}
	var demoQueueCount int64
	gdb.Model(&models.Queue{}).Where("name IN ?", queueNames).Count(&demoQueueCount)
	if demoQueueCount != int64(len(queueSpecs)) {
		t.Errorf("demo queues = %d, want %d", demoQueueCount, len(queueSpecs))
	}
	if got := count(t, gdb, &models.Ticket{}); got != int64(len(ticketSpecs)) {
		t.Errorf("tickets = %d, want %d", got, len(ticketSpecs))
	}
	if got := count(t, gdb, &models.Problem{}); got != 1 {
		t.Errorf("problems = %d, want 1", got)
	}
	if got := count(t, gdb, &models.Workflow{}); got != 1 {
		t.Errorf("workflows = %d, want 1", got)
	}

	var userCount int64
	gdb.Model(&models.User{}).Where("username LIKE ?", usernamePrefix+"%").Count(&userCount)
	if userCount != 11 { // 1 Manager + 4 Engineers + 6 Customers
		t.Errorf("demo users = %d, want 11", userCount)
	}

	if got := count(t, gdb, &models.Service{}); got != int64(len(serviceSpecs)) {
		t.Errorf("services = %d, want %d", got, len(serviceSpecs))
	}
	if got := count(t, gdb, &models.KBArticle{}); got != 2 {
		t.Errorf("kb articles = %d, want 2", got)
	}
	var publishedCount int64
	gdb.Model(&models.KBArticle{}).Where("status = ?", models.KBStatusPublished).Count(&publishedCount)
	if publishedCount != 1 {
		t.Errorf("published kb articles = %d, want 1", publishedCount)
	}
}

func TestReset_WipesAndReseeds(t *testing.T) {
	gdb := testDB(t)
	log := logging.New("error")
	if err := Seed(gdb, log); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	var firstTicketIDs []int64
	gdb.Model(&models.Ticket{}).Order("id").Pluck("id", &firstTicketIDs)

	if err := Reset(gdb, log); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if got := count(t, gdb, &models.Ticket{}); got != int64(len(ticketSpecs)) {
		t.Errorf("tickets after reset = %d, want %d", got, len(ticketSpecs))
	}
	if got := count(t, gdb, &models.Organization{}); got != 3 {
		t.Errorf("organizations after reset = %d, want 3", got)
	}
	if got := count(t, gdb, &models.Service{}); got != int64(len(serviceSpecs)) {
		t.Errorf("services after reset = %d, want %d", got, len(serviceSpecs))
	}
	if got := count(t, gdb, &models.KBArticle{}); got != 2 {
		t.Errorf("kb articles after reset = %d, want 2", got)
	}

	var secondTicketIDs []int64
	gdb.Model(&models.Ticket{}).Order("id").Pluck("id", &secondTicketIDs)
	if len(firstTicketIDs) > 0 && len(secondTicketIDs) > 0 && firstTicketIDs[0] == secondTicketIDs[0] {
		t.Error("expected Reset to wipe old rows and insert new ones (IDs should not be reused)")
	}
}

// TestSeed_DoesNotDisturbExistingUserOne guards the ordering constraint that
// matters in cmd/servicedesk/main.go: whoever creates the first row in the
// users table lands on ID 1 (see auth.SystemActorID). Seed itself doesn't
// enforce this - main.go must call it after auth.Bootstrap - but this test
// documents the assumption Seed relies on: it never assumes it's creating
// the first user.
func TestSeed_DoesNotDisturbExistingUserOne(t *testing.T) {
	gdb := testDB(t)
	if err := gdb.Create(&models.User{Username: "system", Role: models.RoleSystemAdmin, Source: "system"}).Error; err != nil {
		t.Fatalf("seed system user: %v", err)
	}

	if err := Seed(gdb, logging.New("error")); err != nil {
		t.Fatalf("Seed: %v", err)
	}

	var systemUser models.User
	if err := gdb.First(&systemUser, 1).Error; err != nil {
		t.Fatalf("get user 1: %v", err)
	}
	if systemUser.Username != "system" {
		t.Fatalf("user 1 = %q, want %q (Seed must not touch existing rows)", systemUser.Username, "system")
	}
}
