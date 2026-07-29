// SPDX-License-Identifier: AGPL-3.0
//
// Package notify provides multi-channel notifications for the
// federated-meetup platform. It supports three channels — push, email,
// and SMS — with graceful degradation: the Notifier tries push first,
// falls back to email, then SMS.
//
// Each channel is independently toggleable via user preferences and
// provider configuration. Provider credentials come from environment
// variables, never hardcoded.
package notify

import (
	"context"
	"fmt"
	"log"
	"time"
)

// ---- Channel interfaces ----

// PushSender sends a web push notification to a single subscription.
type PushSender interface {
	// Send pushes a notification with the given title and body to the
	// push subscription identified by subscriptionJSON.
	Send(ctx context.Context, subscriptionJSON string, title, body string) error
}

// EmailSender sends an email notification to a single recipient.
type EmailSender interface {
	Send(ctx context.Context, to, subject, body string) error
}

// SMSSender sends an SMS notification to a single phone number.
type SMSSender interface {
	Send(ctx context.Context, toPhoneNumber, body string) error
}

// ---- User profile (for routing decisions) ----

// UserProfile holds the information needed to deliver a notification
// to a user across channels. Fields that are empty/nil indicate the
// channel is unavailable for that user.
type UserProfile struct {
	Email            string // email address (required for email channel)
	Phone            string // phone number in E.164 format (required for SMS)
	PushSubscription string // web push subscription JSON (required for push)
	// Preferences
	NotifyEmail bool
	NotifySMS   bool
	NotifyPush  bool
}

// ---- Notifier ----

// Notifier sends a notification to a user via the best available channel.
// It tries push → email → SMS, respecting user preferences and provider
// availability.
type Notifier struct {
	push  PushSender
	email EmailSender
	sms   SMSSender
}

// NewNotifier constructs a Notifier. Any sender may be nil — the
// Notifier will skip nil channels during degradation.
func NewNotifier(push PushSender, email EmailSender, sms SMSSender) *Notifier {
	return &Notifier{push: push, email: email, sms: sms}
}

// Send delivers a notification to the user. It tries channels in order:
// push → email → SMS, skipping channels that are disabled in the user's
// preferences or that have no provider configured. Returns nil if at
// least one channel succeeds; returns an error only if all enabled
// channels fail or none are available.
func (n *Notifier) Send(ctx context.Context, user UserProfile, subject, body string) error {
	// Try push first (highest priority, least intrusive if already seen).
	if user.NotifyPush && user.PushSubscription != "" && n.push != nil {
		if err := n.push.Send(ctx, user.PushSubscription, subject, body); err != nil {
			log.Printf("notify: push failed for user %s: %v", user.Email, err)
		} else {
			return nil
		}
	}

	// Fall back to email.
	if user.NotifyEmail && user.Email != "" && n.email != nil {
		if err := n.email.Send(ctx, user.Email, subject, body); err != nil {
			log.Printf("notify: email failed for user %s: %v", user.Email, err)
		} else {
			return nil
		}
	}

	// Last resort: SMS.
	if user.NotifySMS && user.Phone != "" && n.sms != nil {
		if err := n.sms.Send(ctx, user.Phone, body); err != nil {
			log.Printf("notify: SMS failed for user %s: %v", user.Email, err)
			} else {
				return nil
			}
	}

	return fmt.Errorf("notify: all channels failed or unavailable for user %s", user.Email)
}

// HasPush returns true if a push sender is configured.
func (n *Notifier) HasPush() bool { return n.push != nil }

// HasEmail returns true if an email sender is configured.
func (n *Notifier) HasEmail() bool { return n.email != nil }

// HasSMS returns true if an SMS sender is configured.
func (n *Notifier) HasSMS() bool { return n.sms != nil }

// ---- Notification types ----

// NotificationType identifies the kind of notification being sent.
// Used to check group-level notification preferences.
type NotificationType string

const (
	NotifyNewEvent      NotificationType = "new_event"
	NotifyNewMessage    NotificationType = "new_message"
	NotifyRsvpReminder   NotificationType = "rsvp_reminder"
	NotifyEventReminder  NotificationType = "event_reminder"
	NotifyRsvpConfirm    NotificationType = "rsvp_confirmation"
	NotifyScheduleChange NotificationType = "schedule_change"
)

// ---- Quiet hours ----

// QuietHours defines a time window during which notifications are
// suppressed (queued for later delivery).
type QuietHours struct {
	Start string // "HH:MM" 24-hour format, e.g. "22:00"
	End   string // "HH:MM" 24-hour format, e.g. "07:00"
}

// IsQuiet returns true if the given time falls within the quiet hours
// window. If Start == End or both are empty, quiet hours are disabled.
// Handles overnight windows (e.g. 22:00 → 07:00).
func (qh QuietHours) IsQuiet(t time.Time) bool {
	if qh.Start == "" || qh.End == "" || qh.Start == qh.End {
		return false
	}
	now := t.Format("15:04")
	// If start > end, the window crosses midnight (e.g. 22:00 → 07:00).
	if qh.Start > qh.End {
		return now >= qh.Start || now < qh.End
	}
	// Same-day window (e.g. 12:00 → 14:00).
	return now >= qh.Start && now < qh.End
}