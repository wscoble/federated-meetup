// SPDX-License-Identifier: AGPL-3.0
//
// Package email provides email sending for the federated-meetup host.
// It defines an EmailSender interface with three implementations:
//
//   - SMTPSender   — production sender via Go's net/smtp package
//   - LogOnlySender — dev sender that logs to stdout (no network)
//   - NoopSender    — test sender that does nothing (for unit tests)
package email

import (
	"context"
	"fmt"
	"log"
	"net/smtp"
	"strings"
)

// EmailSender is the interface all email senders implement.
// ctx is used by the SMTP sender to honour cancellation; the log and
// noop senders ignore it.
type EmailSender interface {
	// Send delivers a plain-text email to the given recipient.
	// to, subject, and body must be non-empty.
	Send(ctx context.Context, to, subject, body string) error
}

// ---- SMTP implementation ----

// SMTPConfig holds the connection parameters for an SMTP server.
type SMTPConfig struct {
	Host     string // SMTP host, e.g. "smtp.gmail.com"
	Port     string // SMTP port, e.g. "587"
	Username string // SMTP auth username
	Password string // SMTP auth password
	From     string // From: address, e.g. "noreply@example.com"
}

// SMTPSender sends email over SMTP using Go's net/smtp package.
type SMTPSender struct {
	cfg SMTPConfig
}

// NewSMTPSender constructs an SMTPSender from a config.
func NewSMTPSender(cfg SMTPConfig) *SMTPSender {
	return &SMTPSender{cfg: cfg}
}

// Send formats and sends a plain-text email via SMTP.
// It uses PLAIN auth and a basic RFC 5322 message structure.
func (s *SMTPSender) Send(ctx context.Context, to, subject, body string) error {
	if s.cfg.Host == "" {
		return fmt.Errorf("email: SMTP host not configured")
	}
	addr := s.cfg.Host + ":" + s.cfg.Port

	// Build RFC 5322 message
	msg := buildMessage(s.cfg.From, to, subject, body)

	// Use PLAIN auth if username is set
	var auth smtp.Auth
	if s.cfg.Username != "" {
		auth = smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	}

	// Honour context cancellation (best-effort; net/smtp doesn't accept ctx)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	return smtp.SendMail(addr, auth, s.cfg.From, []string{to}, []byte(msg))
}

// ---- LogOnly implementation (dev) ----

// LogOnlySender logs the email to stdout instead of sending it.
// This is the behaviour that existed before email wiring — useful for
// local development without an SMTP server.
type LogOnlySender struct{}

// NewLogOnlySender returns a LogOnlySender.
func NewLogOnlySender() *LogOnlySender { return &LogOnlySender{} }

// Send logs the email details to stdout.
func (s *LogOnlySender) Send(_ context.Context, to, subject, body string) error {
	log.Printf("email[log-only]: to=%s subject=%q\n%s", to, subject, body)
	return nil
}

// ---- Noop implementation (tests) ----

// NoopSender is a no-op sender for unit tests. It records the last
// sent email so tests can assert on it.
type NoopSender struct {
	LastTo      string
	LastSubject string
	LastBody    string
	SendCount   int
}

// NewNoopSender returns a NoopSender.
func NewNoopSender() *NoopSender { return &NoopSender{} }

// Send records the email details and returns nil.
func (s *NoopSender) Send(_ context.Context, to, subject, body string) error {
	s.LastTo = to
	s.LastSubject = subject
	s.LastBody = body
	s.SendCount++
	return nil
}

// ---- Message formatting ----

// buildMessage constructs a minimal RFC 5322 email message.
func buildMessage(from, to, subject, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", to)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(body)
	return b.String()
}