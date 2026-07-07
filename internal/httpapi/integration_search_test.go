package httpapi

import (
	"net/url"
	"strings"
	"testing"
)

func TestSearch_FindsTicketByTitleAndNoteBody(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Payment gateway timeout"}, "description": {"customers cannot checkout"},
		"queue_id": {"1"}, "priority": {"P1"}, "category": {"payments"},
	})
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Printer jam"}, "description": {"office printer"},
		"queue_id": {"1"}, "priority": {"P4"}, "category": {"office"},
	})
	admin.mustPost(t, "/tickets/2/notes", url.Values{"body": {"turns out it needs a firmware update"}})

	byTitle := bodyString(t, admin.get("/tickets?q=gateway"))
	if !strings.Contains(byTitle, "Payment gateway timeout") {
		t.Fatal("search by title term did not find the ticket")
	}
	if strings.Contains(byTitle, "Printer jam") {
		t.Fatal("search by title term should not match unrelated ticket")
	}

	byNote := bodyString(t, admin.get("/tickets?q=firmware"))
	if !strings.Contains(byNote, "Printer jam") {
		t.Fatal("search over note body did not find the ticket (DESIGN.md 3.7: search across notes too)")
	}
}
