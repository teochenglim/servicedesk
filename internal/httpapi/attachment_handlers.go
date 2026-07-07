package httpapi

import (
	"io"
	"mime"
	"net/http"
	"strconv"

	"servicedesk/internal/middleware"
	"servicedesk/internal/service"
)

func (s *Server) handleAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := ticketIDFromPath(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	t, err := s.ticketSvc.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !s.customerCanSeeTicket(claims, t) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.attachmentSvc.MaxSize()+1<<20) // +1MB of multipart overhead
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, "upload too large or malformed", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "could not read file", http.StatusBadRequest)
		return
	}

	if _, err := s.attachmentSvc.Upload(claims, id, nil, header.Filename, data); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	redirectToTicket(w, r, id)
}

func (s *Server) handleAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	claims := middleware.ClaimsFrom(r.Context())
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	a, err := s.attachmentSvc.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	t, err := s.ticketSvc.Get(a.TicketID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !s.customerCanSeeTicket(claims, t) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if !service.CanView(claims, a) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	disposition := "attachment"
	if service.IsInlineable(a.MIMEType) {
		disposition = "inline"
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": a.Filename}))
	w.Header().Set("Content-Type", a.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(a.SizeBytes, 10))
	w.Write(a.Data)
}
