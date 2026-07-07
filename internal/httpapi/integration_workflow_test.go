package httpapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRunbook_UserInputHttpRequestTemplateRender(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"deploys": []string{"v1.2.3", "v1.2.2"}})
	}))
	defer upstream.Close()

	admin.mustPost(t, "/tickets", url.Values{
		"title": {"API latency spike"}, "description": {"p99 up"}, "queue_id": {"1"}, "priority": {"P2"}, "category": {"infra"},
	})

	config := `{"steps":[
		{"id":"gather_info","type":"user_input","fields":[{"name":"service_name","label":"Service","required":true}]},
		{"id":"fetch_deploys","type":"http_request","url":"` + upstream.URL + `","save_response_to":"deploy_events"},
		{"id":"draft_note","type":"template_render","template":"Incident opener for {{.service_name}}","output_target":"ticket_external_note"}
	]}`
	admin.mustPost(t, "/admin/workflows", url.Values{
		"name": {"Incident Runbook"}, "trigger": {"ticket_created"}, "is_runbook": {"on"}, "config": {config},
	})

	admin.mustPost(t, "/tickets/1/runbooks/1/start", nil)
	env.engine.ProcessOne() // executes gather_info -> pauses at user_input

	body := bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "needs input") {
		t.Fatal("expected the runbook to be paused waiting for user_input")
	}

	admin.mustPost(t, "/workflow-tasks/1/resume", url.Values{"service_name": {"checkout-api"}})
	env.engine.ProcessOne() // fetch_deploys (http_request) + draft_note (template_render)

	body = bodyString(t, admin.get("/tickets/1"))
	if !strings.Contains(body, "Incident opener for checkout-api") {
		t.Fatal("expected the rendered template to be posted as an external note")
	}
}

func TestApproval_RejectionStopsWorkflow(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")
	createUser(t, admin, "qadmin", "q@x.com", "pass123", "Manager")

	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Escalation request"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P1"}, "category": {"x"},
	})

	config := `{"steps":[{"id":"approve_escalation","type":"approval","approver_role":"Manager"},{"id":"note","type":"add_note","body":"escalated"}]}`
	admin.mustPost(t, "/admin/workflows", url.Values{
		"name": {"Escalation"}, "trigger": {"ticket_created"}, "is_runbook": {"on"}, "config": {config},
	})
	admin.mustPost(t, "/tickets/1/runbooks/1/start", nil)
	env.engine.ProcessOne() // creates the approval, pauses

	qadmin := env.client()
	qadmin.mustLogin("", "qadmin", "pass123")
	qadmin.mustPost(t, "/approvals/1/decide", url.Values{"decision": {"reject"}})

	env.engine.ProcessOne() // should fail the task, not add the note

	body := bodyString(t, admin.get("/tickets/1"))
	if strings.Contains(body, "escalated") {
		t.Fatal("rejected approval must not let the workflow continue to add_note")
	}
}

func TestWebhook_DeliversSignedPayloadOnTicketCreated(t *testing.T) {
	env := newTestEnv(t)
	admin := env.client()
	admin.mustLogin("", "admin", "admin123")

	received := make(chan string, 1)
	secret := "whsec-test"
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(buf)
		want := hex.EncodeToString(mac.Sum(nil))
		if r.Header.Get("X-ServiceDesk-Signature") != want {
			w.WriteHeader(http.StatusUnauthorized)
			received <- "BAD SIGNATURE"
			return
		}
		received <- string(buf)
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	admin.mustPost(t, "/admin/webhooks", url.Values{
		"url": {receiver.URL}, "events": {"ticket.created"}, "secret": {secret},
	})
	admin.mustPost(t, "/tickets", url.Values{
		"title": {"Webhook test ticket"}, "description": {"d"}, "queue_id": {"1"}, "priority": {"P3"}, "category": {"x"},
	})

	env.whDispatcher.ProcessOne()

	select {
	case payload := <-received:
		if payload == "BAD SIGNATURE" {
			t.Fatal("webhook signature did not verify")
		}
		if !strings.Contains(payload, "Webhook test ticket") {
			t.Fatalf("payload missing ticket data: %s", payload)
		}
	default:
		t.Fatal("webhook receiver never got a delivery")
	}
}
