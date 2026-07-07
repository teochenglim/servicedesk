package service

import (
	"errors"
	"testing"

	"servicedesk/internal/auth"
	"servicedesk/internal/db"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

// newTestAttachmentService wires a real in-memory sqlite DB (not a mock) -
// AttachmentService.Upload writes through repo.AttachmentRepo/NoteRepo, which
// need an actual *gorm.DB, so this is the lightest self-contained setup that
// still exercises the real validation logic.
func newTestAttachmentService(t *testing.T, maxSize int64) (*AttachmentService, *repo.NoteRepo) {
	t.Helper()
	gdb, err := db.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := gdb.DB()
		sqlDB.Close()
	})
	attachments := repo.NewAttachmentRepo(gdb)
	notes := repo.NewNoteRepo(gdb)
	return NewAttachmentService(attachments, notes, maxSize), notes
}

// TestCanView_StaffSeesEverything covers "engineer and manager can see all"
// regardless of who uploaded it or whether it's on an internal note.
func TestCanView_StaffSeesEverything(t *testing.T) {
	for _, role := range []models.Role{models.RoleEngineer, models.RoleManager, models.RoleSystemAdmin, models.RoleAgent} {
		viewer := &auth.Claims{UserID: 99, Role: role}
		cases := []*models.Attachment{
			{UploaderID: 1, Internal: true, CustomerPrivate: false},
			{UploaderID: 1, Internal: false, CustomerPrivate: true},
			{UploaderID: 2, Internal: true, CustomerPrivate: true},
		}
		for _, a := range cases {
			if !CanView(viewer, a) {
				t.Errorf("%s should see every attachment, got denied for %+v", role, a)
			}
		}
	}
}

// TestCanView_CustomerCannotSeeInternal mirrors the existing internal-note rule.
func TestCanView_CustomerCannotSeeInternal(t *testing.T) {
	viewer := &auth.Claims{UserID: 1, Role: models.RoleCustomer}
	a := &models.Attachment{UploaderID: 1, Internal: true}
	if CanView(viewer, a) {
		t.Error("Customer should never see an attachment on an internal note, even their own upload")
	}
}

// TestCanView_CustomerPrivateUploadsAreOwnerOnly is the new requirement: a
// Customer's own upload is invisible to other Customers, even ones who can
// otherwise see the same ticket (e.g. a same-org watcher), while a
// staff-uploaded external attachment stays visible to any Customer.
func TestCanView_CustomerPrivateUploadsAreOwnerOnly(t *testing.T) {
	uploader := &auth.Claims{UserID: 1, Role: models.RoleCustomer}
	otherCustomer := &auth.Claims{UserID: 2, Role: models.RoleCustomer}

	customerUpload := &models.Attachment{UploaderID: 1, Internal: false, CustomerPrivate: true}
	if !CanView(uploader, customerUpload) {
		t.Error("uploader should always see their own attachment")
	}
	if CanView(otherCustomer, customerUpload) {
		t.Error("a different Customer must not see another Customer's upload")
	}

	staffUpload := &models.Attachment{UploaderID: 42, Internal: false, CustomerPrivate: false}
	if !CanView(otherCustomer, staffUpload) {
		t.Error("a staff-uploaded external attachment must remain visible to any Customer who can see the ticket")
	}
}

func TestUpload_RejectsOversizedAndDisallowedTypes(t *testing.T) {
	svc, _ := newTestAttachmentService(t, 10) // tiny cap on purpose
	actor := &auth.Claims{UserID: 1, Role: models.RoleEngineer}

	if _, err := svc.Upload(actor, 1, nil, "big.png", make([]byte, 20)); !errors.Is(err, ErrAttachmentTooLarge) {
		t.Errorf("Upload(20 bytes, 10-byte cap) error = %v, want ErrAttachmentTooLarge", err)
	}

	svc2, _ := newTestAttachmentService(t, 1<<20)
	if _, err := svc2.Upload(actor, 1, nil, "evil.exe", []byte("MZ")); !errors.Is(err, ErrAttachmentTypeNotAllowed) {
		t.Errorf("Upload(.exe) error = %v, want ErrAttachmentTypeNotAllowed", err)
	}
}

// TestUpload_InheritsInternalAndMarksCustomerPrivate covers both DESIGN/08
// §8.7 rules together: a note-scoped attachment inherits Internal from its
// parent note, and a Customer's upload is flagged CustomerPrivate so CanView
// can keep it private to them among other Customers.
func TestUpload_InheritsInternalAndMarksCustomerPrivate(t *testing.T) {
	svc, notes := newTestAttachmentService(t, 1<<20)

	n := &models.Note{TicketID: 1, AuthorID: 7, Body: "internal diagnosis", Internal: true}
	if err := notes.Create(n); err != nil {
		t.Fatalf("create note: %v", err)
	}

	customer := &auth.Claims{UserID: 5, Role: models.RoleCustomer}
	a, err := svc.Upload(customer, 1, &n.ID, "shot.png", make([]byte, 100))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !a.Internal {
		t.Error("attachment should inherit Internal=true from its parent note")
	}
	if !a.CustomerPrivate {
		t.Error("a Customer's upload should be marked CustomerPrivate")
	}

	engineer := &auth.Claims{UserID: 8, Role: models.RoleEngineer}
	a2, err := svc.Upload(engineer, 1, nil, "diagram.png", make([]byte, 100))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if a2.Internal {
		t.Error("a ticket-level attachment (no parent note) should default to external")
	}
	if a2.CustomerPrivate {
		t.Error("a staff upload should never be marked CustomerPrivate")
	}
}

// TestUpload_RejectsNoteFromAnotherTicket guards the cross-ticket mixup case.
func TestUpload_RejectsNoteFromAnotherTicket(t *testing.T) {
	svc, notes := newTestAttachmentService(t, 1<<20)
	n := &models.Note{TicketID: 2, AuthorID: 1, Body: "note on ticket 2", Internal: false}
	if err := notes.Create(n); err != nil {
		t.Fatalf("create note: %v", err)
	}
	actor := &auth.Claims{UserID: 1, Role: models.RoleEngineer}
	if _, err := svc.Upload(actor, 1, &n.ID, "file.txt", []byte("hello")); !errors.Is(err, ErrForbidden) {
		t.Errorf("uploading against a note from a different ticket: err = %v, want ErrForbidden", err)
	}
}
