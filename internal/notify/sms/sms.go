// SPDX-License-Identifier: AGPL-3.0
//
// Package sms provides SMS notification delivery via HTTP-based SMS
// providers (Twilio, Telnyx). No SDKs are used — just net/http.
//
// Provider credentials are read from environment variables:
//
//	TWILIO_ACCOUNT_SID   — Twilio account SID
//	TWILIO_AUTH_TOKEN    — Twilio auth token
//	TWILIO_FROM_NUMBER   — Twilio sender phone number
//
//	TELNYX_API_KEY       — Telnyx API key
//	TELNYX_FROM_NUMBER   — Telnyx sender phone number
package sms

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// SMSSender is the interface for SMS providers.
type SMSSender interface {
	Send(ctx context.Context, to, body string) error
}

// ---- Twilio ----

// TwilioConfig holds the configuration for the Twilio SMS provider.
type TwilioConfig struct {
	AccountSID string
	AuthToken  string
	FromNumber string
}

// TwilioConfigFromEnv reads Twilio config from environment variables.
func TwilioConfigFromEnv() TwilioConfig {
	return TwilioConfig{
		AccountSID: os.Getenv("TWILIO_ACCOUNT_SID"),
		AuthToken:  os.Getenv("TWILIO_AUTH_TOKEN"),
		FromNumber: os.Getenv("TWILIO_FROM_NUMBER"),
	}
}

// TwilioSender sends SMS via the Twilio REST API.
type TwilioSender struct {
	cfg TwilioConfig
	hc  *http.Client
}

// NewTwilioSender constructs a TwilioSender. If cfg fields are empty,
// Send will return an error.
func NewTwilioSender(cfg TwilioConfig) *TwilioSender {
	return &TwilioSender{cfg: cfg, hc: http.DefaultClient}
}

// Send delivers an SMS via the Twilio Messages API.
func (s *TwilioSender) Send(ctx context.Context, to, body string) error {
	if s.cfg.AccountSID == "" || s.cfg.AuthToken == "" {
		return fmt.Errorf("sms/twilio: credentials not configured")
	}
	if s.cfg.FromNumber == "" {
		return fmt.Errorf("sms/twilio: from number not configured")
	}
	if to == "" {
		return fmt.Errorf("sms/twilio: recipient is empty")
	}

	url := fmt.Sprintf("https://api.twilio.com/2010-04-01/Accounts/%s/Messages.json", s.cfg.AccountSID)

	form := fmt.Sprintf("To=%s&From=%s&Body=%s", to, s.cfg.FromNumber, escapeForm(body))

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(form))
	if err != nil {
		return fmt.Errorf("sms/twilio: build request: %w", err)
	}
	req.SetBasicAuth(s.cfg.AccountSID, s.cfg.AuthToken)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("sms/twilio: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("sms/twilio: API returned %d", resp.StatusCode)
	}
	return nil
}

// ---- Telnyx ----

// TelnyxConfig holds the configuration for the Telnyx SMS provider.
type TelnyxConfig struct {
	APIKey     string
	FromNumber string
}

// TelnyxConfigFromEnv reads Telnyx config from environment variables.
func TelnyxConfigFromEnv() TelnyxConfig {
	return TelnyxConfig{
		APIKey:     os.Getenv("TELNYX_API_KEY"),
		FromNumber: os.Getenv("TELNYX_FROM_NUMBER"),
	}
}

// TelnyxSender sends SMS via the Telnyx Messaging API.
type TelnyxSender struct {
	cfg TelnyxConfig
	hc  *http.Client
}

// NewTelnyxSender constructs a TelnyxSender.
func NewTelnyxSender(cfg TelnyxConfig) *TelnyxSender {
	return &TelnyxSender{cfg: cfg, hc: http.DefaultClient}
}

// Send delivers an SMS via the Telnyx Messaging API.
func (s *TelnyxSender) Send(ctx context.Context, to, body string) error {
	if s.cfg.APIKey == "" {
		return fmt.Errorf("sms/telnyx: API key not configured")
	}
	if s.cfg.FromNumber == "" {
		return fmt.Errorf("sms/telnyx: from number not configured")
	}
	if to == "" {
		return fmt.Errorf("sms/telnyx: recipient is empty")
	}

	jsonBody := fmt.Sprintf(`{"from":"%s","to":"%s","text":"%s"}`,
		escapeJSON(s.cfg.FromNumber), escapeJSON(to), escapeJSON(body))

	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://api.telnyx.com/v2/messages", strings.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("sms/telnyx: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.hc.Do(req)
	if err != nil {
		return fmt.Errorf("sms/telnyx: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("sms/telnyx: API returned %d", resp.StatusCode)
	}
	return nil
}

// ---- Noop sender (tests/dev) ----

// NoopSender is a no-op SMS sender for tests. It records the last sent
// message.
type NoopSender struct {
	LastTo   string
	LastBody string
	Calls    int
}

// NewNoopSender returns a NoopSender.
func NewNoopSender() *NoopSender { return &NoopSender{} }

// Send records the message and returns nil.
func (s *NoopSender) Send(_ context.Context, to, body string) error {
	s.LastTo = to
	s.LastBody = body
	s.Calls++
	return nil
}

// ---- Provider selection ----

// NewFromEnv creates an SMS sender from environment variables. It tries
// Twilio first, then Telnyx. Returns nil if neither is configured.
func NewFromEnv() SMSSender {
	twilio := TwilioConfigFromEnv()
	if twilio.AccountSID != "" && twilio.AuthToken != "" {
		return NewTwilioSender(twilio)
	}
	telnyx := TelnyxConfigFromEnv()
	if telnyx.APIKey != "" {
		return NewTelnyxSender(telnyx)
	}
	return nil
}

// ---- Helpers ----

func escapeForm(s string) string {
	s = strings.ReplaceAll(s, "&", "%26")
	s = strings.ReplaceAll(s, "=", "%3D")
	s = strings.ReplaceAll(s, " ", "+")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	return s
}