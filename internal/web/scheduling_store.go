// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ---- Schema migrations for scheduling features ----

// migrateScheduling creates tables for availability polls, waitlist,
// and recurrence exceptions. Called by Store.migrate() after base schema.
func (s *Store) migrateScheduling() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS availability_polls (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			group_key      TEXT NOT NULL DEFAULT '',
			event_id       TEXT NOT NULL DEFAULT '',
			proposed_times TEXT NOT NULL DEFAULT '[]',
			created_at     INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_polls_event ON availability_polls(group_key, event_id)`,
		`CREATE TABLE IF NOT EXISTS poll_votes (
			poll_id          INTEGER NOT NULL DEFAULT 0,
			voter_email      TEXT NOT NULL DEFAULT '',
			voter_name       TEXT NOT NULL DEFAULT '',
			time_slot_index  INTEGER NOT NULL DEFAULT 0,
			voted_at         INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (poll_id, voter_email, time_slot_index)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_poll_votes_poll ON poll_votes(poll_id)`,
		`CREATE TABLE IF NOT EXISTS waitlist_entries (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			group_key   TEXT NOT NULL DEFAULT '',
			event_id    TEXT NOT NULL DEFAULT '',
			email       TEXT NOT NULL DEFAULT '',
			name        TEXT NOT NULL DEFAULT '',
			joined_at   INTEGER NOT NULL DEFAULT 0,
			notified_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_waitlist_event ON waitlist_entries(group_key, event_id, joined_at)`,
		`CREATE TABLE IF NOT EXISTS recurrence_exceptions (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			group_key       TEXT NOT NULL DEFAULT '',
			recurrence_id   TEXT NOT NULL DEFAULT '',
			exception_date  TEXT NOT NULL DEFAULT '',
			reason          TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_exceptions_recurrence ON recurrence_exceptions(group_key, recurrence_id)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
	}
	return nil
}

// ---- Availability polls ----

// AvailabilityPoll represents a poll for preferred meeting times.
type AvailabilityPoll struct {
	ID            int64
	GroupKey      string
	EventID       string
	ProposedTimes []int64 // unix timestamps
	CreatedAt     int64
}

// CreatePoll creates a new availability poll for an event.
// Returns the created poll with its ID.
func (s *Store) CreatePoll(p AvailabilityPoll) (AvailabilityPoll, error) {
	if len(p.ProposedTimes) == 0 {
		return AvailabilityPoll{}, fmt.Errorf("at least one proposed time is required")
	}
	timesJSON, err := json.Marshal(p.ProposedTimes)
	if err != nil {
		return AvailabilityPoll{}, fmt.Errorf("marshal proposed_times: %w", err)
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = s.now().Unix()
	}
	res, err := s.db.Exec(
		`INSERT INTO availability_polls (group_key, event_id, proposed_times, created_at) VALUES (?, ?, ?, ?)`,
		p.GroupKey, p.EventID, string(timesJSON), p.CreatedAt,
	)
	if err != nil {
		return AvailabilityPoll{}, err
	}
	id, _ := res.LastInsertId()
	p.ID = id
	return p, nil
}

// GetPollForEvent returns the most recent poll for a given event.
func (s *Store) GetPollForEvent(groupKey, eventID string) (AvailabilityPoll, error) {
	var p AvailabilityPoll
	var timesJSON string
	err := s.db.QueryRow(
		`SELECT id, group_key, event_id, proposed_times, created_at FROM availability_polls
		 WHERE group_key = ? AND event_id = ? ORDER BY created_at DESC LIMIT 1`,
		groupKey, eventID,
	).Scan(&p.ID, &p.GroupKey, &p.EventID, &timesJSON, &p.CreatedAt)
	if err != nil {
		return AvailabilityPoll{}, err
	}
	if err := json.Unmarshal([]byte(timesJSON), &p.ProposedTimes); err != nil {
		return AvailabilityPoll{}, fmt.Errorf("unmarshal proposed_times: %w", err)
	}
	return p, nil
}

// GetPollByID returns a poll by its ID.
func (s *Store) GetPollByID(pollID int64) (AvailabilityPoll, error) {
	var p AvailabilityPoll
	var timesJSON string
	err := s.db.QueryRow(
		`SELECT id, group_key, event_id, proposed_times, created_at FROM availability_polls WHERE id = ?`,
		pollID,
	).Scan(&p.ID, &p.GroupKey, &p.EventID, &timesJSON, &p.CreatedAt)
	if err != nil {
		return AvailabilityPoll{}, err
	}
	if err := json.Unmarshal([]byte(timesJSON), &p.ProposedTimes); err != nil {
		return AvailabilityPoll{}, fmt.Errorf("unmarshal proposed_times: %w", err)
	}
	return p, nil
}

// ---- Poll votes ----

// PollVote is a single vote on a poll time slot.
type PollVote struct {
	PollID         int64
	VoterEmail     string
	VoterName      string
	TimeSlotIndex  int
	VotedAt        int64
}

// CastVote records (or updates) a vote for a time slot.
// A voter can vote for multiple slots but only once per slot.
func (s *Store) CastVote(v PollVote) error {
	if v.VotedAt == 0 {
		v.VotedAt = s.now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO poll_votes (poll_id, voter_email, voter_name, time_slot_index, voted_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(poll_id, voter_email, time_slot_index) DO UPDATE SET
		   voter_name=excluded.voter_name,
		   voted_at=excluded.voted_at`,
		v.PollID, v.VoterEmail, v.VoterName, v.TimeSlotIndex, v.VotedAt,
	)
	return err
}

// ListVotesForPoll returns all votes for a poll.
func (s *Store) ListVotesForPoll(pollID int64) ([]PollVote, error) {
	rows, err := s.db.Query(
		`SELECT poll_id, voter_email, voter_name, time_slot_index, voted_at FROM poll_votes
		 WHERE poll_id = ? ORDER BY voted_at ASC`,
		pollID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PollVote
	for rows.Next() {
		var v PollVote
		if err := rows.Scan(&v.PollID, &v.VoterEmail, &v.VoterName, &v.TimeSlotIndex, &v.VotedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// VoteCountForSlot returns the number of votes for a specific time slot.
func (s *Store) VoteCountForSlot(pollID int64, slotIndex int) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM poll_votes WHERE poll_id = ? AND time_slot_index = ?`,
		pollID, slotIndex,
	).Scan(&count)
	return count, err
}

// ---- Waitlist ----

// WaitlistEntry is a single entry on an event waitlist.
type WaitlistEntry struct {
	ID        int64
	GroupKey  string
	EventID   string
	Email     string
	Name      string
	JoinedAt  int64
	NotifiedAt int64
}

// JoinWaitlist adds a person to the waitlist for an event.
func (s *Store) JoinWaitlist(w WaitlistEntry) (WaitlistEntry, error) {
	if w.JoinedAt == 0 {
		w.JoinedAt = s.now().Unix()
	}
	res, err := s.db.Exec(
		`INSERT INTO waitlist_entries (group_key, event_id, email, name, joined_at, notified_at)
		 VALUES (?, ?, ?, ?, ?, 0)`,
		w.GroupKey, w.EventID, w.Email, w.Name, w.JoinedAt,
	)
	if err != nil {
		return WaitlistEntry{}, err
	}
	id, _ := res.LastInsertId()
	w.ID = id
	return w, nil
}

// ListWaitlist returns all waitlist entries for an event, ordered by join time.
func (s *Store) ListWaitlist(groupKey, eventID string) ([]WaitlistEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, group_key, event_id, email, name, joined_at, notified_at FROM waitlist_entries
		 WHERE group_key = ? AND event_id = ? ORDER BY joined_at ASC`,
		groupKey, eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WaitlistEntry
	for rows.Next() {
		var w WaitlistEntry
		if err := rows.Scan(&w.ID, &w.GroupKey, &w.EventID, &w.Email, &w.Name, &w.JoinedAt, &w.NotifiedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// PromoteFromWaitlist promotes the next person on the waitlist (earliest join time).
// Marks them as notified and returns the promoted entry. Returns sql.ErrNoRows if
// the waitlist is empty.
func (s *Store) PromoteFromWaitlist(groupKey, eventID string) (WaitlistEntry, error) {
	var w WaitlistEntry
	err := s.db.QueryRow(
		`SELECT id, group_key, event_id, email, name, joined_at, notified_at FROM waitlist_entries
		 WHERE group_key = ? AND event_id = ? ORDER BY joined_at ASC LIMIT 1`,
		groupKey, eventID,
	).Scan(&w.ID, &w.GroupKey, &w.EventID, &w.Email, &w.Name, &w.JoinedAt, &w.NotifiedAt)
	if err != nil {
		return WaitlistEntry{}, err
	}
	_, err = s.db.Exec(
		`UPDATE waitlist_entries SET notified_at = ? WHERE id = ?`,
		s.now().Unix(), w.ID,
	)
	if err != nil {
		return WaitlistEntry{}, err
	}
	w.NotifiedAt = s.now().Unix()
	return w, nil
}

// WaitlistCount returns the number of people on the waitlist for an event.
func (s *Store) WaitlistCount(groupKey, eventID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM waitlist_entries WHERE group_key = ? AND event_id = ?`,
		groupKey, eventID,
	).Scan(&count)
	return count, err
}

// ---- Recurrence exceptions ----

// RecurrenceException marks a specific date to skip in a recurring event series.
type RecurrenceException struct {
	ID            int64
	GroupKey      string
	RecurrenceID  string
	ExceptionDate string // YYYY-MM-DD
	Reason        string
}

// AddRecurrenceException adds an exception date to a recurring event series.
func (s *Store) AddRecurrenceException(e RecurrenceException) (RecurrenceException, error) {
	if e.RecurrenceID == "" {
		return RecurrenceException{}, fmt.Errorf("recurrence_id is required")
	}
	if e.ExceptionDate == "" {
		return RecurrenceException{}, fmt.Errorf("exception_date is required")
	}
	res, err := s.db.Exec(
		`INSERT INTO recurrence_exceptions (group_key, recurrence_id, exception_date, reason)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT DO NOTHING`,
		e.GroupKey, e.RecurrenceID, e.ExceptionDate, e.Reason,
	)
	if err != nil {
		return RecurrenceException{}, err
	}
	id, _ := res.LastInsertId()
	e.ID = id
	return e, nil
}

// ListRecurrenceExceptions returns all exception dates for a recurrence series.
func (s *Store) ListRecurrenceExceptions(groupKey, recurrenceID string) ([]RecurrenceException, error) {
	rows, err := s.db.Query(
		`SELECT id, group_key, recurrence_id, exception_date, reason FROM recurrence_exceptions
		 WHERE group_key = ? AND recurrence_id = ? ORDER BY exception_date ASC`,
		groupKey, recurrenceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RecurrenceException
	for rows.Next() {
		var e RecurrenceException
		if err := rows.Scan(&e.ID, &e.GroupKey, &e.RecurrenceID, &e.ExceptionDate, &e.Reason); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// IsDateExcepted checks if a specific date is in the exception list for a recurrence.
func (s *Store) IsDateExcepted(groupKey, recurrenceID, date string) (bool, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM recurrence_exceptions WHERE group_key = ? AND recurrence_id = ? AND exception_date = ?`,
		groupKey, recurrenceID, date,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// Ensure time and database/sql are used (for lint compliance when imports are only used in some build configs).
var _ = time.Now
var _ = sql.ErrNoRows