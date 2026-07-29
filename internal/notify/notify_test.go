// SPDX-License-Identifier: AGPL-3.0
package notify

import (
	"context"
	"testing"
	"time"
)

// ---- Mock senders ----

type mockPushSender struct {
	calls    int
	lastSub  string
	lastTit  string
	lastBody string
	fail     bool
}

func (m *mockPushSender) Send(_ context.Context, sub, title, body string) error {
	m.calls++
	m.lastSub = sub
	m.lastTit = title
	m.lastBody = body
	if m.fail {
		return errMock
	}
	return nil
}

type mockEmailSender struct {
	calls       int
	lastTo      string
	lastSubject string
	lastBody    string
	fail        bool
}

func (m *mockEmailSender) Send(_ context.Context, to, subject, body string) error {
	m.calls++
	m.lastTo = to
	m.lastSubject = subject
	m.lastBody = body
	if m.fail {
		return errMock
	}
	return nil
}

type mockSMSSender struct {
	calls    int
	lastTo   string
	lastBody string
	fail     bool
}

func (m *mockSMSSender) Send(_ context.Context, to, body string) error {
	m.calls++
	m.lastTo = to
	m.lastBody = body
	if m.fail {
		return errMock
	}
	return nil
}

var errMock = errMockSentinel{}

type errMockSentinel struct{}

func (errMockSentinel) Error() string { return "mock send error" }

// ---- Tests ----

func TestNotifierPushSucceeds(t *testing.T) {
	push := &mockPushSender{}
	email := &mockEmailSender{}
	sms := &mockSMSSender{}
	n := NewNotifier(push, email, sms)

	user := UserProfile{
		Email:            "alice@example.com",
		PushSubscription: `{"endpoint":"https://example.com/push/123"}`,
		NotifyEmail:      true,
		NotifyPush:       true,
		NotifySMS:        true,
	}

	if err := n.Send(context.Background(), user, "Test", "Hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if push.calls != 1 {
		t.Fatalf("push.calls = %d, want 1", push.calls)
	}
	if email.calls != 0 {
		t.Fatalf("email.calls = %d, want 0 (push succeeded)", email.calls)
	}
	if sms.calls != 0 {
		t.Fatalf("sms.calls = %d, want 0", sms.calls)
	}
}

func TestNotifierPushFailFallsBackToEmail(t *testing.T) {
	push := &mockPushSender{fail: true}
	email := &mockEmailSender{}
	sms := &mockSMSSender{}
	n := NewNotifier(push, email, sms)

	user := UserProfile{
		Email:            "alice@example.com",
		PushSubscription: `{"endpoint":"https://example.com/push/123"}`,
		NotifyEmail:      true,
		NotifyPush:       true,
		NotifySMS:        true,
	}

	if err := n.Send(context.Background(), user, "Test", "Hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if push.calls != 1 {
		t.Fatalf("push.calls = %d, want 1", push.calls)
	}
	if email.calls != 1 {
		t.Fatalf("email.calls = %d, want 1 (fallback)", email.calls)
	}
	if sms.calls != 0 {
		t.Fatalf("sms.calls = %d, want 0", sms.calls)
	}
}

func TestNotifierEmailFailFallsBackToSMS(t *testing.T) {
	push := &mockPushSender{fail: true}
	email := &mockEmailSender{fail: true}
	sms := &mockSMSSender{}
	n := NewNotifier(push, email, sms)

	user := UserProfile{
		Email:            "alice@example.com",
		Phone:            "+15551234567",
		PushSubscription: `{"endpoint":"https://example.com/push/123"}`,
		NotifyEmail:      true,
		NotifyPush:       true,
		NotifySMS:        true,
	}

	if err := n.Send(context.Background(), user, "Test", "Hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if push.calls != 1 {
		t.Fatalf("push.calls = %d, want 1", push.calls)
	}
	if email.calls != 1 {
		t.Fatalf("email.calls = %d, want 1", email.calls)
	}
	if sms.calls != 1 {
		t.Fatalf("sms.calls = %d, want 1 (last resort)", sms.calls)
	}
}

func TestNotifierAllFail(t *testing.T) {
	push := &mockPushSender{fail: true}
	email := &mockEmailSender{fail: true}
	sms := &mockSMSSender{fail: true}
	n := NewNotifier(push, email, sms)

	user := UserProfile{
		Email:            "alice@example.com",
		Phone:            "+15551234567",
		PushSubscription: `{"endpoint":"https://example.com/push/123"}`,
		NotifyEmail:      true,
		NotifyPush:       true,
		NotifySMS:        true,
	}

	if err := n.Send(context.Background(), user, "Test", "Hello"); err == nil {
		t.Fatal("expected error when all channels fail")
	}
}

func TestNotifierRespectsPreferences(t *testing.T) {
	push := &mockPushSender{}
	email := &mockEmailSender{}
	sms := &mockSMSSender{}
	n := NewNotifier(push, email, sms)

	// User has push disabled, email enabled.
	user := UserProfile{
		Email:            "alice@example.com",
		PushSubscription: `{"endpoint":"https://example.com/push/123"}`,
		NotifyEmail:      true,
		NotifyPush:       false,
		NotifySMS:        false,
	}

	if err := n.Send(context.Background(), user, "Test", "Hello"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if push.calls != 0 {
		t.Fatalf("push.calls = %d, want 0 (push disabled)", push.calls)
	}
	if email.calls != 1 {
		t.Fatalf("email.calls = %d, want 1", email.calls)
	}
	if sms.calls != 0 {
		t.Fatalf("sms.calls = %d, want 0 (sms disabled)", sms.calls)
	}
}

func TestNotifierNilSenders(t *testing.T) {
	n := NewNotifier(nil, nil, nil)
	user := UserProfile{
		Email:       "alice@example.com",
		NotifyEmail: true,
		NotifyPush:  true,
		NotifySMS:   true,
	}
	if err := n.Send(context.Background(), user, "Test", "Hello"); err == nil {
		t.Fatal("expected error when no senders configured")
	}
}

// ---- Quiet hours tests ----

func TestQuietHoursOvernight(t *testing.T) {
	qh := QuietHours{Start: "22:00", End: "07:00"}

	// 23:00 is quiet.
	if !qh.IsQuiet(time.Date(2026, 7, 15, 23, 0, 0, 0, time.UTC)) {
		t.Fatal("23:00 should be quiet")
	}
	// 03:00 is quiet.
	if !qh.IsQuiet(time.Date(2026, 7, 15, 3, 0, 0, 0, time.UTC)) {
		t.Fatal("03:00 should be quiet")
	}
	// 12:00 is not quiet.
	if qh.IsQuiet(time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)) {
		t.Fatal("12:00 should not be quiet")
	}
	// 08:00 is not quiet.
	if qh.IsQuiet(time.Date(2026, 7, 15, 8, 0, 0, 0, time.UTC)) {
		t.Fatal("08:00 should not be quiet")
	}
}

func TestQuietHoursSameDay(t *testing.T) {
	qh := QuietHours{Start: "12:00", End: "14:00"}

	// 13:00 is quiet.
	if !qh.IsQuiet(time.Date(2026, 7, 15, 13, 0, 0, 0, time.UTC)) {
		t.Fatal("13:00 should be quiet (same-day window)")
	}
	// 11:00 is not quiet.
	if qh.IsQuiet(time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)) {
		t.Fatal("11:00 should not be quiet")
	}
	// 15:00 is not quiet.
	if qh.IsQuiet(time.Date(2026, 7, 15, 15, 0, 0, 0, time.UTC)) {
		t.Fatal("15:00 should not be quiet")
	}
}

func TestQuietHoursDisabled(t *testing.T) {
	qh := QuietHours{} // both empty
	if qh.IsQuiet(time.Date(2026, 7, 15, 23, 0, 0, 0, time.UTC)) {
		t.Fatal("23:00 should not be quiet when disabled")
	}

	qh2 := QuietHours{Start: "09:00", End: "09:00"} // start == end
	if qh2.IsQuiet(time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)) {
		t.Fatal("should not be quiet when start == end")
	}
}

func TestQuietHoursBoundary(t *testing.T) {
	qh := QuietHours{Start: "22:00", End: "07:00"}

	// Exactly 22:00 is quiet (inclusive start).
	if !qh.IsQuiet(time.Date(2026, 7, 15, 22, 0, 0, 0, time.UTC)) {
		t.Fatal("22:00 should be quiet (inclusive start)")
	}
	// Exactly 07:00 is not quiet (exclusive end).
	if qh.IsQuiet(time.Date(2026, 7, 15, 7, 0, 0, 0, time.UTC)) {
		t.Fatal("07:00 should not be quiet (exclusive end)")
	}
}