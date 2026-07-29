// SPDX-License-Identifier: AGPL-3.0
package notify

import (
	"database/sql"
	"fmt"
)

// UserNotifyPrefs holds per-user notification preferences.
type UserNotifyPrefs struct {
	UserEmail   string
	NotifyEmail bool // default true
	NotifySMS   bool // default false
	NotifyPush  bool // default false
}

// GroupNotifyPrefs holds per-group notification preferences.
type GroupNotifyPrefs struct {
	GroupKey            string
	NotifyOnNewEvent    bool // default true
	NotifyOnNewMessage  bool // default true
	NotifyOnRsvpReminder bool // default true
	QuietHoursStart     string // "HH:MM", empty = no quiet hours
	QuietHoursEnd       string // "HH:MM", empty = no quiet hours
}

// PrefsStore manages notification preferences in SQLite.
type PrefsStore struct {
	db *sql.DB
}

// NewPrefsStore creates a PrefsStore, creating the tables if needed.
func NewPrefsStore(db *sql.DB) (*PrefsStore, error) {
	if db == nil {
		return nil, fmt.Errorf("notify: db is nil")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS user_notify_prefs (
			user_email TEXT PRIMARY KEY,
			notify_email INTEGER NOT NULL DEFAULT 1,
			notify_sms INTEGER NOT NULL DEFAULT 0,
			notify_push INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS group_notify_prefs (
			group_key TEXT PRIMARY KEY,
			notify_on_new_event INTEGER NOT NULL DEFAULT 1,
			notify_on_new_message INTEGER NOT NULL DEFAULT 1,
			notify_on_rsvp_reminder INTEGER NOT NULL DEFAULT 1,
			quiet_hours_start TEXT NOT NULL DEFAULT '',
			quiet_hours_end TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return nil, fmt.Errorf("notify: create prefs table: %w", err)
		}
	}
	return &PrefsStore{db: db}, nil
}

// ---- User preferences ----

// GetUserPrefs returns the notification preferences for a user.
// If no preferences are stored, returns defaults (email=true, others=false).
func (p *PrefsStore) GetUserPrefs(userEmail string) (UserNotifyPrefs, error) {
	var prefs UserNotifyPrefs
	var email, sms, push int
	err := p.db.QueryRow(
		`SELECT user_email, notify_email, notify_sms, notify_push FROM user_notify_prefs WHERE user_email = ?`,
		userEmail,
	).Scan(&prefs.UserEmail, &email, &sms, &push)
	if err == sql.ErrNoRows {
		return UserNotifyPrefs{
			UserEmail:   userEmail,
			NotifyEmail: true,
			NotifySMS:   false,
			NotifyPush:  false,
		}, nil
	}
	if err != nil {
		return UserNotifyPrefs{}, fmt.Errorf("notify: get user prefs: %w", err)
	}
	prefs.NotifyEmail = email != 0
	prefs.NotifySMS = sms != 0
	prefs.NotifyPush = push != 0
	return prefs, nil
}

// SetUserPrefs sets the notification preferences for a user.
func (p *PrefsStore) SetUserPrefs(prefs UserNotifyPrefs) error {
	email := 0
	if prefs.NotifyEmail {
		email = 1
	}
	sms := 0
	if prefs.NotifySMS {
		sms = 1
	}
	push := 0
	if prefs.NotifyPush {
		push = 1
	}
	_, err := p.db.Exec(
		`INSERT INTO user_notify_prefs (user_email, notify_email, notify_sms, notify_push)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(user_email) DO UPDATE SET
		   notify_email=excluded.notify_email,
		   notify_sms=excluded.notify_sms,
		   notify_push=excluded.notify_push`,
		prefs.UserEmail, email, sms, push,
	)
	if err != nil {
		return fmt.Errorf("notify: set user prefs: %w", err)
	}
	return nil
}

// ---- Group preferences ----

// GetGroupPrefs returns the notification preferences for a group.
// If no preferences are stored, returns defaults (all enabled, no quiet hours).
func (p *PrefsStore) GetGroupPrefs(groupKey string) (GroupNotifyPrefs, error) {
	var prefs GroupNotifyPrefs
	var newEvent, newMsg, rsvpReminder int
	err := p.db.QueryRow(
		`SELECT group_key, notify_on_new_event, notify_on_new_message, notify_on_rsvp_reminder,
		        quiet_hours_start, quiet_hours_end
		 FROM group_notify_prefs WHERE group_key = ?`,
		groupKey,
	).Scan(&prefs.GroupKey, &newEvent, &newMsg, &rsvpReminder, &prefs.QuietHoursStart, &prefs.QuietHoursEnd)
	if err == sql.ErrNoRows {
		return GroupNotifyPrefs{
			GroupKey:             groupKey,
			NotifyOnNewEvent:     true,
			NotifyOnNewMessage:   true,
			NotifyOnRsvpReminder: true,
		}, nil
	}
	if err != nil {
		return GroupNotifyPrefs{}, fmt.Errorf("notify: get group prefs: %w", err)
	}
	prefs.NotifyOnNewEvent = newEvent != 0
	prefs.NotifyOnNewMessage = newMsg != 0
	prefs.NotifyOnRsvpReminder = rsvpReminder != 0
	return prefs, nil
}

// SetGroupPrefs sets the notification preferences for a group.
func (p *PrefsStore) SetGroupPrefs(prefs GroupNotifyPrefs) error {
	newEvent := 0
	if prefs.NotifyOnNewEvent {
		newEvent = 1
	}
	newMsg := 0
	if prefs.NotifyOnNewMessage {
		newMsg = 1
	}
	rsvpReminder := 0
	if prefs.NotifyOnRsvpReminder {
		rsvpReminder = 1
	}
	_, err := p.db.Exec(
		`INSERT INTO group_notify_prefs (group_key, notify_on_new_event, notify_on_new_message, notify_on_rsvp_reminder, quiet_hours_start, quiet_hours_end)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(group_key) DO UPDATE SET
		   notify_on_new_event=excluded.notify_on_new_event,
		   notify_on_new_message=excluded.notify_on_new_message,
		   notify_on_rsvp_reminder=excluded.notify_on_rsvp_reminder,
		   quiet_hours_start=excluded.quiet_hours_start,
		   quiet_hours_end=excluded.quiet_hours_end`,
		prefs.GroupKey, newEvent, newMsg, rsvpReminder, prefs.QuietHoursStart, prefs.QuietHoursEnd,
	)
	if err != nil {
		return fmt.Errorf("notify: set group prefs: %w", err)
	}
	return nil
}