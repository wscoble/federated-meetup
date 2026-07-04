// SPDX-License-Identifier: AGPL-3.0
package email

import (
	"context"
	"strings"
	"testing"
)

func TestNoopSender(t *testing.T) {
	s := NoopSender{}
	if err := s.Send(context.Background(), "alice@example.com", "Test", "Hello"); err != nil {
		t.Fatalf("NoopSender.Send: %v", err)
	}
	if s.LastTo != "alice@example.com" {
		t.Fatalf("LastTo = %q, want alice@example.com", s.LastTo)
	}
	if s.LastSubject != "Test" {
		t.Fatalf("LastSubject = %q, want Test", s.LastSubject)
	}
	if s.LastBody != "Hello" {
		t.Fatalf("LastBody = %q, want Hello", s.LastBody)
	}
	if s.SendCount != 1 {
		t.Fatalf("SendCount = %d, want 1", s.SendCount)
	}
}

func TestLogOnlySender(t *testing.T) {
	s := LogOnlySender{}
	if err := s.Send(context.Background(), "bob@example.com", "Subject", "Body"); err != nil {
		t.Fatalf("LogOnlySender.Send: %v", err)
	}
}

func TestSMTPSenderNoHost(t *testing.T) {
	s := SMTPSender{cfg: SMTPConfig{}}
	if err := s.Send(context.Background(), "a@b.com", "x", "y"); err == nil {
		t.Fatal("expected error when SMTP host is empty")
	}
}

func TestSMTPSenderCancelledContext(t *testing.T) {
	s := SMTPSender{cfg: SMTPConfig{Host: "smtp.example.com", Port: "587"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Send(ctx, "a@b.com", "x", "y"); err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestBuildMessage(t *testing.T) {
	msg := buildMessage("from@example.com", "to@example.com", "Test Subject", "Hello body")
	if !strings.Contains(msg, "From: from@example.com\r\n") {
		t.Fatal("missing From header")
	}
	if !strings.Contains(msg, "To: to@example.com\r\n") {
		t.Fatal("missing To header")
	}
	if !strings.Contains(msg, "Subject: Test Subject\r\n") {
		t.Fatal("missing Subject header")
	}
	if !strings.Contains(msg, "MIME-Version: 1.0\r\n") {
		t.Fatal("missing MIME-Version header")
	}
	if !strings.Contains(msg, "Content-Type: text/plain; charset=utf-8\r\n") {
		t.Fatal("missing Content-Type header")
	}
	if !strings.HasSuffix(msg, "Hello body") {
		t.Fatal("body should be at the end")
	}
}

func TestRenderRsvpConfirm(t *testing.T) {
	data := RsvpConfirmData{
		EventTitle:    "Go Night",
		EventDate:     "Mon, Jul 15, 2026 6:00 PM",
		EventLocation: "Innovation Center",
		MagicLink:     "http://localhost:8091/rsvp/abc123",
		GroupName:     "Vegas Programmers",
	}
	subject, body, err := RenderRsvpConfirm(data)
	if err != nil {
		t.Fatalf("RenderRsvpConfirm: %v", err)
	}
	if !strings.Contains(subject, "Confirm your RSVP for Go Night") {
		t.Fatalf("unexpected subject: %s", subject)
	}
	if !strings.Contains(body, "Go Night") {
		t.Fatal("body should contain event title")
	}
	if !strings.Contains(body, "http://localhost:8091/rsvp/abc123") {
		t.Fatal("body should contain magic link")
	}
	if !strings.Contains(body, "Innovation Center") {
		t.Fatal("body should contain location")
	}
	if !strings.Contains(body, "Vegas Programmers") {
		t.Fatal("body should contain group name")
	}
}

func TestRenderRsvpConfirmed(t *testing.T) {
	data := RsvpConfirmedData{
		EventTitle:    "Rust vs Go",
		EventDate:     "Tue, Jul 22, 2026 7:00 PM",
		EventLocation: "Springs Preserve",
		GroupName:     "Vegas Programmers",
		CancelURL:     "http://localhost:8091/my-rsvps",
	}
	subject, body, err := RenderRsvpConfirmed(data)
	if err != nil {
		t.Fatalf("RenderRsvpConfirmed: %v", err)
	}
	if !strings.Contains(subject, "Your RSVP is confirmed for Rust vs Go") {
		t.Fatalf("unexpected subject: %s", subject)
	}
	if !strings.Contains(body, "Rust vs Go") {
		t.Fatal("body should contain event title")
	}
	if !strings.Contains(body, "Springs Preserve") {
		t.Fatal("body should contain location")
	}
	if !strings.Contains(body, "http://localhost:8091/my-rsvps") {
		t.Fatal("body should contain cancel URL")
	}
}

func TestRenderEventReminder(t *testing.T) {
	data := EventReminderData{
		EventTitle:    "Hackathon",
		EventDate:     "Sat, Aug 1, 2026 9:00 AM",
		EventLocation: "UNLV",
		GroupName:     "Vegas Programmers",
		EventURL:      "http://localhost:8091/events/g/evt-hack",
	}
	subject, body, err := RenderEventReminder(data)
	if err != nil {
		t.Fatalf("RenderEventReminder: %v", err)
	}
	if !strings.Contains(subject, "Reminder: Hackathon tomorrow at") {
		t.Fatalf("unexpected subject: %s", subject)
	}
	if !strings.Contains(body, "Hackathon") {
		t.Fatal("body should contain event title")
	}
	if !strings.Contains(body, "UNLV") {
		t.Fatal("body should contain location")
	}
	if !strings.Contains(body, "http://localhost:8091/events/g/evt-hack") {
		t.Fatal("body should contain event URL")
	}
}

func TestRenderOrganizerNotify(t *testing.T) {
	data := OrganizerNotifyData{
		EventTitle: "Go Night",
		Name:       "Alice",
		Email:      "alice@example.com",
		EventDate:  "Mon, Jul 15, 2026 6:00 PM",
	}
	subject, body, err := RenderOrganizerNotify(data)
	if err != nil {
		t.Fatalf("RenderOrganizerNotify: %v", err)
	}
	if !strings.Contains(subject, "New RSVP for Go Night from Alice") {
		t.Fatalf("unexpected subject: %s", subject)
	}
	if !strings.Contains(body, "Alice") {
		t.Fatal("body should contain name")
	}
	if !strings.Contains(body, "alice@example.com") {
		t.Fatal("body should contain email")
	}
}

func TestFormatEventDate(t *testing.T) {
	// 2026-07-15 18:00 UTC
	got := FormatEventDate(1790174400)
	if !strings.Contains(got, "2026") {
		t.Fatalf("expected year in output, got %s", got)
	}
}

func TestFormatTimeOnly(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"Mon, Jul 15, 2026 6:00 PM", "6:00 PM"},
		{"6:00 PM", "6:00 PM"},
		{"", ""},
	}
	for _, c := range cases {
		got := formatTimeOnly(c.input)
		if got != c.want {
			t.Errorf("formatTimeOnly(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestNewSMTPSender(t *testing.T) {
	cfg := SMTPConfig{Host: "smtp.test.com", Port: "587", Username: "u", Password: "p", From: "noreply@test.com"}
	s := NewSMTPSender(cfg)
	if s.cfg.Host != "smtp.test.com" {
		t.Fatalf("host = %s, want smtp.test.com", s.cfg.Host)
	}
}

func TestNewLogOnlySender(t *testing.T) {
	s := NewLogOnlySender()
	if s == nil {
		t.Fatal("NewLogOnlySender returned nil")
	}
}

func TestNewNoopSender(t *testing.T) {
	s := NewNoopSender()
	if s == nil {
		t.Fatal("NewNoopSender returned nil")
	}
}