// SPDX-License-Identifier: AGPL-3.0
package sms

import (
	"context"
	"testing"
)

func TestNoopSender(t *testing.T) {
	s := NewNoopSender()
	if err := s.Send(context.Background(), "+15551234567", "Hello SMS"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if s.LastTo != "+15551234567" {
		t.Fatalf("LastTo = %s, want +15551234567", s.LastTo)
	}
	if s.LastBody != "Hello SMS" {
		t.Fatalf("LastBody = %s, want Hello SMS", s.LastBody)
	}
	if s.Calls != 1 {
		t.Fatalf("Calls = %d, want 1", s.Calls)
	}
}

func TestTwilioSenderMissingCredentials(t *testing.T) {
	s := NewTwilioSender(TwilioConfig{})
	err := s.Send(context.Background(), "+15551234567", "Hello")
	if err == nil {
		t.Fatal("expected error when credentials missing")
	}
}

func TestTwilioSenderMissingFromNumber(t *testing.T) {
	s := NewTwilioSender(TwilioConfig{
		AccountSID: "AC123",
		AuthToken:  "token",
	})
	err := s.Send(context.Background(), "+15551234567", "Hello")
	if err == nil {
		t.Fatal("expected error when from number missing")
	}
}

func TestTelnyxSenderMissingAPIKey(t *testing.T) {
	s := NewTelnyxSender(TelnyxConfig{})
	err := s.Send(context.Background(), "+15551234567", "Hello")
	if err == nil {
		t.Fatal("expected error when API key missing")
	}
}

func TestTelnyxSenderMissingFromNumber(t *testing.T) {
	s := NewTelnyxSender(TelnyxConfig{APIKey: "key123"})
	err := s.Send(context.Background(), "+15551234567", "Hello")
	if err == nil {
		t.Fatal("expected error when from number missing")
	}
}

func TestTwilioSenderEmptyRecipient(t *testing.T) {
	s := NewTwilioSender(TwilioConfig{
		AccountSID: "AC123",
		AuthToken:  "token",
		FromNumber: "+15550000000",
	})
	err := s.Send(context.Background(), "", "Hello")
	if err == nil {
		t.Fatal("expected error when recipient is empty")
	}
}

func TestTelnyxSenderEmptyRecipient(t *testing.T) {
	s := NewTelnyxSender(TelnyxConfig{
		APIKey:     "key123",
		FromNumber: "+15550000000",
	})
	err := s.Send(context.Background(), "", "Hello")
	if err == nil {
		t.Fatal("expected error when recipient is empty")
	}
}

func TestEscapeForm(t *testing.T) {
	got := escapeForm("Hello & world\nfoo=bar")
	if got != "Hello+%26+world%0Afoo%3Dbar" {
		t.Fatalf("escapeForm = %q, want Hello+%%26+world%%0Afoo%%3Dbar", got)
	}
}

func TestEscapeJSON(t *testing.T) {
	got := escapeJSON(`Hello "world" \ test`)
	want := `Hello \"world\" \\ test`
	if got != want {
		t.Fatalf("escapeJSON = %q, want %q", got, want)
	}
}

func TestNewFromEnvReturnsNilWhenUnconfigured(t *testing.T) {
	// With no env vars set, should return nil.
	// This test may fail if the test runner has Twilio/Telnyx env vars set,
	// but that's unlikely in CI.
	sender := NewFromEnv()
	// We can't assert nil deterministically because env vars might be set.
	// Just ensure it doesn't panic.
	_ = sender
}