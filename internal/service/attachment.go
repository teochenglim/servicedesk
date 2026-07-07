package service

import (
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"servicedesk/internal/auth"
	"servicedesk/internal/models"
	"servicedesk/internal/repo"
)

var (
	ErrAttachmentTooLarge       = errors.New("attachment exceeds max upload size")
	ErrAttachmentTypeNotAllowed = errors.New("attachment file type not allowed")
)

// allowedAttachmentExts is an allowlist, not a denylist - arbitrary file
// upload (especially script-like content later served back to a browser) is
// a real attack surface. Covers DESIGN/08 §8.7's examples: screenshots,
// documents, logs, short screen recordings.
var allowedAttachmentExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".pdf": true, ".txt": true, ".log": true, ".csv": true,
	".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".ppt": true, ".pptx": true,
	".mp4": true, ".webm": true, ".mov": true,
}

// sniffedImageTypes are the content-types http.DetectContentType can reliably
// identify for the "render as an inline thumbnail" path (DESIGN/08 §8.7).
// Everything else downloads instead, regardless of its claimed extension -
// see AttachmentService.IsInlineable.
var sniffedImageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

type AttachmentService struct {
	attachments *repo.AttachmentRepo
	notes       *repo.NoteRepo
	maxSize     int64
}

func NewAttachmentService(attachments *repo.AttachmentRepo, notes *repo.NoteRepo, maxSizeBytes int64) *AttachmentService {
	return &AttachmentService{attachments: attachments, notes: notes, maxSize: maxSizeBytes}
}

// MaxSize is the configured per-file upload cap, exposed so the HTTP layer
// can wrap the request body in http.MaxBytesReader before even parsing the
// multipart form - rejecting an oversized upload during Upload's own check
// is too late to stop the server from buffering/reading it all first.
func (s *AttachmentService) MaxSize() int64 { return s.maxSize }

// Upload validates and stores a new attachment either directly against a
// ticket (noteID nil - e.g. the submission form) or a specific note
// (inheriting that note's internal/external visibility, DESIGN/08 §8.7).
func (s *AttachmentService) Upload(actor *auth.Claims, ticketID int64, noteID *int64, filename string, data []byte) (*models.Attachment, error) {
	if int64(len(data)) > s.maxSize {
		return nil, fmt.Errorf("%w: %d bytes (max %d)", ErrAttachmentTooLarge, len(data), s.maxSize)
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !allowedAttachmentExts[ext] {
		return nil, fmt.Errorf("%w: %s", ErrAttachmentTypeNotAllowed, ext)
	}

	sniffLen := len(data)
	if sniffLen > 512 {
		sniffLen = 512
	}
	mimeType := http.DetectContentType(data[:sniffLen])

	internal := false
	if noteID != nil {
		n, err := s.notes.Get(*noteID)
		if err != nil {
			return nil, fmt.Errorf("parent note not found: %w", err)
		}
		if n.TicketID != ticketID {
			return nil, fmt.Errorf("%w: note does not belong to this ticket", ErrForbidden)
		}
		internal = n.Internal
	}

	a := &models.Attachment{
		TicketID: ticketID, NoteID: noteID, UploaderID: actor.UserID,
		Filename: filename, MIMEType: mimeType, SizeBytes: int64(len(data)),
		Internal: internal, CustomerPrivate: actor.Role == models.RoleCustomer,
		StorageBackend: "db", Data: data,
	}
	if err := s.attachments.Create(a); err != nil {
		return nil, err
	}
	return a, nil
}

// Get loads a full row (including its Data blob) for the download path -
// callers must check CanView before serving it.
func (s *AttachmentService) Get(id int64) (*models.Attachment, error) {
	return s.attachments.Get(id)
}

// ListVisibleForTicket returns the attachments on a ticket that viewer is
// allowed to see, already filtered - see CanView for the exact rule.
func (s *AttachmentService) ListVisibleForTicket(viewer *auth.Claims, ticketID int64) ([]models.Attachment, error) {
	all, err := s.attachments.ListMetaForTicket(ticketID)
	if err != nil {
		return nil, err
	}
	visible := make([]models.Attachment, 0, len(all))
	for _, a := range all {
		if CanView(viewer, &a) {
			visible = append(visible, a)
		}
	}
	return visible, nil
}

// CanView decides whether viewer may see a given attachment (list or
// download), independent of whether they can see the ticket at all - callers
// must still enforce the usual ticket-visibility rule (org/creator/watcher
// for Customers) separately, same as for notes/tickets themselves.
//
// Staff (Engineer/Manager/SystemAdmin/Agent - Role.IsAgent) see everything,
// regardless of who uploaded it. A Customer sees external attachments,
// except another Customer's upload stays private to that uploader even if
// both can otherwise see the ticket (e.g. a same-org watcher) - a
// staff-uploaded attachment on an external note is unaffected by this and
// stays visible to any Customer who can see the ticket.
func CanView(viewer *auth.Claims, a *models.Attachment) bool {
	if viewer.Role.IsAgent() {
		return true
	}
	if a.Internal {
		return false
	}
	if a.CustomerPrivate && a.UploaderID != viewer.UserID {
		return false
	}
	return true
}

// IsInlineable reports whether an attachment should render as an <img>
// thumbnail (sniffed as one of the well-known image types) rather than force
// a download - see sniffedImageTypes.
func IsInlineable(mimeType string) bool { return sniffedImageTypes[mimeType] }
