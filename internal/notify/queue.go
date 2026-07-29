// SPDX-License-Identifier: AGPL-3.0
package notify

import (
	"database/sql"
	"fmt"
	"time"
)

// QueueItem represents a row in the notification_queue table.
type QueueItem struct {
	ID          int64
	UserEmail   string
	Channel     string // "push", "email", or "sms"
	Subject     string
	Body        string
	ScheduledAt int64 // unix timestamp
	SentAt      int64 // unix timestamp, 0 if not sent
	Status      string // "pending", "sent", "failed"
}

// Queue is a SQLite-backed notification queue.
type Queue struct {
	db  *sql.DB
	now func() time.Time
}

// NewQueue creates a Queue, creating the notification_queue table if
// it doesn't exist.
func NewQueue(db *sql.DB) (*Queue, error) {
	if db == nil {
		return nil, fmt.Errorf("notify: db is nil")
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS notification_queue (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_email TEXT NOT NULL DEFAULT '',
		channel TEXT NOT NULL DEFAULT 'email',
		subject TEXT NOT NULL DEFAULT '',
		body TEXT NOT NULL DEFAULT '',
		scheduled_at INTEGER NOT NULL DEFAULT 0,
		sent_at INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT 'pending'
	)`); err != nil {
		return nil, fmt.Errorf("notify: create notification_queue: %w", err)
	}
	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_notify_queue_status ON notification_queue(status, scheduled_at)`); err != nil {
		return nil, fmt.Errorf("notify: create index: %w", err)
	}
	return &Queue{db: db, now: time.Now}, nil
}

// SetClock overrides the time source (test-only).
func (q *Queue) SetClock(now func() time.Time) { q.now = now }

// Enqueue adds a notification to the queue for later processing.
func (q *Queue) Enqueue(userEmail, channel, subject, body string, scheduledAt int64) (int64, error) {
	res, err := q.db.Exec(
		`INSERT INTO notification_queue (user_email, channel, subject, body, scheduled_at, sent_at, status)
		 VALUES (?, ?, ?, ?, ?, 0, 'pending')`,
		userEmail, channel, subject, body, scheduledAt,
	)
	if err != nil {
		return 0, fmt.Errorf("notify: enqueue: %w", err)
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// EnqueueNow adds a notification to the queue with scheduled_at = now.
func (q *Queue) EnqueueNow(userEmail, channel, subject, body string) (int64, error) {
	return q.Enqueue(userEmail, channel, subject, body, q.now().Unix())
}

// ListPending returns up to limit items that are pending and whose
// scheduled_at time has passed.
func (q *Queue) ListPending(limit int) ([]QueueItem, error) {
	now := q.now().Unix()
	rows, err := q.db.Query(
		`SELECT id, user_email, channel, subject, body, scheduled_at, sent_at, status
		 FROM notification_queue
		 WHERE status = 'pending' AND scheduled_at <= ?
		 ORDER BY scheduled_at ASC
		 LIMIT ?`,
		now, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("notify: list pending: %w", err)
	}
	defer rows.Close()
	var out []QueueItem
	for rows.Next() {
		var item QueueItem
		if err := rows.Scan(&item.ID, &item.UserEmail, &item.Channel, &item.Subject, &item.Body, &item.ScheduledAt, &item.SentAt, &item.Status); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// MarkSent marks a queue item as successfully sent.
func (q *Queue) MarkSent(id int64) error {
	_, err := q.db.Exec(
		`UPDATE notification_queue SET status = 'sent', sent_at = ? WHERE id = ?`,
		q.now().Unix(), id,
	)
	return err
}

// MarkFailed marks a queue item as failed.
func (q *Queue) MarkFailed(id int64) error {
	_, err := q.db.Exec(
		`UPDATE notification_queue SET status = 'failed', sent_at = ? WHERE id = ?`,
		q.now().Unix(), id,
	)
	return err
}

// CountByStatus returns the count of items with the given status.
func (q *Queue) CountByStatus(status string) (int, error) {
	var count int
	err := q.db.QueryRow(
		`SELECT COUNT(*) FROM notification_queue WHERE status = ?`, status,
	).Scan(&count)
	return count, err
}