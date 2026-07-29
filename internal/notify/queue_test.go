// SPDX-License-Identifier: AGPL-3.0
package notify

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func TestQueueEnqueueAndListPending(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	// Enqueue an item scheduled for now.
	id, err := q.EnqueueNow("alice@example.com", "email", "Test", "Hello")
	if err != nil {
		t.Fatalf("EnqueueNow: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero ID")
	}

	// Should appear in pending list.
	items, err := q.ListPending(10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].UserEmail != "alice@example.com" {
		t.Fatalf("UserEmail = %s", items[0].UserEmail)
	}
	if items[0].Status != "pending" {
		t.Fatalf("Status = %s, want pending", items[0].Status)
	}
}

func TestQueueFutureScheduledNotPending(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	// Enqueue an item scheduled for 1 hour from now.
	future := q.now().Add(1 * time.Hour).Unix()
	_, err = q.Enqueue("alice@example.com", "email", "Test", "Hello", future)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Should NOT appear in pending list (scheduled in the future).
	items, err := q.ListPending(10)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("len(items) = %d, want 0 (future-scheduled)", len(items))
	}
}

func TestQueueMarkSent(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	id, _ := q.EnqueueNow("alice@example.com", "email", "Test", "Hello")

	if err := q.MarkSent(id); err != nil {
		t.Fatalf("MarkSent: %v", err)
	}

	// Should no longer be pending.
	items, _ := q.ListPending(10)
	if len(items) != 0 {
		t.Fatalf("after MarkSent, pending items = %d, want 0", len(items))
	}

	count, _ := q.CountByStatus("sent")
	if count != 1 {
		t.Fatalf("sent count = %d, want 1", count)
	}
}

func TestQueueMarkFailed(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	id, _ := q.EnqueueNow("alice@example.com", "email", "Test", "Hello")

	if err := q.MarkFailed(id); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	count, _ := q.CountByStatus("failed")
	if count != 1 {
		t.Fatalf("failed count = %d, want 1", count)
	}
}

func TestQueueWithMockClock(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	// Set a fixed clock.
	fixedTime := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	q.SetClock(func() time.Time { return fixedTime })

	// Enqueue for now.
	_, _ = q.EnqueueNow("alice@example.com", "email", "Test", "Hello")

	items, _ := q.ListPending(10)
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
}

// ---- Processor integration test ----

func TestProcessorSendsNotification(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	push := &mockPushSender{}
	email := &mockEmailSender{}
	sms := &mockSMSSender{}
	notifier := NewNotifier(push, email, sms)

	// Enqueue a notification for a user with email-only prefs.
	_, _ = q.EnqueueNow("alice@example.com", "email", "Test Subject", "Test Body")

	processor := NewProcessor(q, ProcessorConfig{
		Resolver: func(_ context.Context, email string) (UserProfile, error) {
			return UserProfile{
				Email:       email,
				NotifyEmail: true,
			}, nil
		},
	})

	if err := processor.ProcessOnce(context.Background(), notifier); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}

	if email.calls != 1 {
		t.Fatalf("email.calls = %d, want 1", email.calls)
	}
	if email.lastTo != "alice@example.com" {
		t.Fatalf("email.lastTo = %s, want alice@example.com", email.lastTo)
	}

	// Queue item should be marked as sent.
	sentCount, _ := q.CountByStatus("sent")
	if sentCount != 1 {
		t.Fatalf("sent count = %d, want 1", sentCount)
	}
}

func TestProcessorRespectsChannel(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	push := &mockPushSender{}
	email := &mockEmailSender{}
	sms := &mockSMSSender{}
	notifier := NewNotifier(push, email, sms)

	// Enqueue a notification for SMS channel only.
	_, _ = q.EnqueueNow("bob@example.com", "sms", "Test Subject", "Test Body")

	processor := NewProcessor(q, ProcessorConfig{
		Resolver: func(_ context.Context, email string) (UserProfile, error) {
			return UserProfile{
				Email:      email,
				Phone:      "+15551234567",
				NotifySMS:  true,
				NotifyEmail: false,
				NotifyPush: false,
			}, nil
		},
	})

	if err := processor.ProcessOnce(context.Background(), notifier); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}

	if sms.calls != 1 {
		t.Fatalf("sms.calls = %d, want 1", sms.calls)
	}
	if email.calls != 0 {
		t.Fatalf("email.calls = %d, want 0 (sms channel requested)", email.calls)
	}
}

func TestProcessorMarksFailedOnSendError(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	// All senders fail.
	push := &mockPushSender{fail: true}
	email := &mockEmailSender{fail: true}
	sms := &mockSMSSender{fail: true}
	notifier := NewNotifier(push, email, sms)

	_, _ = q.EnqueueNow("alice@example.com", "auto", "Test", "Hello")

	processor := NewProcessor(q, ProcessorConfig{
		Resolver: func(_ context.Context, email string) (UserProfile, error) {
			return UserProfile{
				Email:       email,
				Phone:       "+15551234567",
				NotifyEmail: true,
				NotifyPush:  true,
				NotifySMS:   true,
			}, nil
		},
	})

	_ = processor.ProcessOnce(context.Background(), notifier)

	failedCount, _ := q.CountByStatus("failed")
	if failedCount != 1 {
		t.Fatalf("failed count = %d, want 1", failedCount)
	}
}

func TestProcessorNoItemsDoesNothing(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	q, err := NewQueue(db)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}

	email := &mockEmailSender{}
	notifier := NewNotifier(nil, email, nil)

	processor := NewProcessor(q, ProcessorConfig{
		Resolver: func(_ context.Context, email string) (UserProfile, error) {
			return UserProfile{Email: email, NotifyEmail: true}, nil
		},
	})

	if err := processor.ProcessOnce(context.Background(), notifier); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if email.calls != 0 {
		t.Fatalf("email.calls = %d, want 0 (empty queue)", email.calls)
	}
}

// ---- Prefs tests ----

func TestUserPrefsDefaults(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	p, err := NewPrefsStore(db)
	if err != nil {
		t.Fatalf("NewPrefsStore: %v", err)
	}

	prefs, err := p.GetUserPrefs("newuser@example.com")
	if err != nil {
		t.Fatalf("GetUserPrefs: %v", err)
	}
	if !prefs.NotifyEmail {
		t.Fatal("default NotifyEmail should be true")
	}
	if prefs.NotifySMS {
		t.Fatal("default NotifySMS should be false")
	}
	if prefs.NotifyPush {
		t.Fatal("default NotifyPush should be false")
	}
}

func TestUserPrefsSetAndGet(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	p, err := NewPrefsStore(db)
	if err != nil {
		t.Fatalf("NewPrefsStore: %v", err)
	}

	err = p.SetUserPrefs(UserNotifyPrefs{
		UserEmail:   "alice@example.com",
		NotifyEmail: false,
		NotifySMS:   true,
		NotifyPush:  true,
	})
	if err != nil {
		t.Fatalf("SetUserPrefs: %v", err)
	}

	prefs, err := p.GetUserPrefs("alice@example.com")
	if err != nil {
		t.Fatalf("GetUserPrefs: %v", err)
	}
	if prefs.NotifyEmail {
		t.Fatal("NotifyEmail should be false")
	}
	if !prefs.NotifySMS {
		t.Fatal("NotifySMS should be true")
	}
	if !prefs.NotifyPush {
		t.Fatal("NotifyPush should be true")
	}
}

func TestGroupPrefsDefaults(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	p, err := NewPrefsStore(db)
	if err != nil {
		t.Fatalf("NewPrefsStore: %v", err)
	}

	prefs, err := p.GetGroupPrefs("some-group-key")
	if err != nil {
		t.Fatalf("GetGroupPrefs: %v", err)
	}
	if !prefs.NotifyOnNewEvent {
		t.Fatal("default NotifyOnNewEvent should be true")
	}
	if !prefs.NotifyOnNewMessage {
		t.Fatal("default NotifyOnNewMessage should be true")
	}
	if !prefs.NotifyOnRsvpReminder {
		t.Fatal("default NotifyOnRsvpReminder should be true")
	}
	if prefs.QuietHoursStart != "" {
		t.Fatal("default QuietHoursStart should be empty")
	}
}

func TestGroupPrefsSetAndGet(t *testing.T) {
	db := openTestDB(t)
	defer db.Close()

	p, err := NewPrefsStore(db)
	if err != nil {
		t.Fatalf("NewPrefsStore: %v", err)
	}

	err = p.SetGroupPrefs(GroupNotifyPrefs{
		GroupKey:            "group-123",
		NotifyOnNewEvent:   false,
		NotifyOnNewMessage:  true,
		NotifyOnRsvpReminder: false,
		QuietHoursStart:     "22:00",
		QuietHoursEnd:       "07:00",
	})
	if err != nil {
		t.Fatalf("SetGroupPrefs: %v", err)
	}

	prefs, err := p.GetGroupPrefs("group-123")
	if err != nil {
		t.Fatalf("GetGroupPrefs: %v", err)
	}
	if prefs.NotifyOnNewEvent {
		t.Fatal("NotifyOnNewEvent should be false")
	}
	if !prefs.NotifyOnNewMessage {
		t.Fatal("NotifyOnNewMessage should be true")
	}
	if prefs.NotifyOnRsvpReminder {
		t.Fatal("NotifyOnRsvpReminder should be false")
	}
	if prefs.QuietHoursStart != "22:00" {
		t.Fatalf("QuietHoursStart = %s, want 22:00", prefs.QuietHoursStart)
	}
	if prefs.QuietHoursEnd != "07:00" {
		t.Fatalf("QuietHoursEnd = %s, want 07:00", prefs.QuietHoursEnd)
	}
}