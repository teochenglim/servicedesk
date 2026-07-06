package mailer

import (
	"fmt"
	"log/slog"
	"net/smtp"
)

// Mailer sends plain-text notification emails (DESIGN.md 3.9 / Phase 3 SMTP notifications).
// With no host configured it logs instead of sending, so local/dev runs don't need SMTP.
type Mailer struct {
	host, from, user, pass string
	port                   int
	log                    *slog.Logger
}

func New(host string, port int, from, user, pass string, log *slog.Logger) *Mailer {
	return &Mailer{host: host, port: port, from: from, user: user, pass: pass, log: log}
}

func (m *Mailer) Send(to, subject, body string) error {
	if m.host == "" {
		m.log.Info("mailer: SMTP not configured, skipping send", "to", to, "subject", subject)
		return nil
	}
	addr := fmt.Sprintf("%s:%d", m.host, m.port)
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s\r\n", m.from, to, subject, body)

	var auth smtp.Auth
	if m.user != "" {
		auth = smtp.PlainAuth("", m.user, m.pass, m.host)
	}
	return smtp.SendMail(addr, auth, m.from, []string{to}, []byte(msg))
}
