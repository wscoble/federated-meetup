// SPDX-License-Identifier: AGPL-3.0
//
// Package web provides the server-side rendered HTML frontend for the
// federated-meetup host product (Package C — Ticketed Workshop).
//
// The web layer sits on top of internal/host.Service (protocol) and
// internal/product.Service (ticketing/orders). It renders Go html/template
// pages, serves HTMX partials, and uses SQLite (pure-Go modernc.org/sqlite)
// for a local cache of groups, events, RSVPs, organizer sessions, and orders.
package web

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// DefaultDSN is the SQLite DSN used when no explicit path is provided.
// WAL mode gives concurrent readers + single writer without blocking.
const DefaultDSN = "file:fedmeetup.db?_journal_mode=WAL&_busy_timeout=5000"

// Store is the SQLite-backed persistence layer for the web frontend.
// It caches group/event data from the protocol layer and stores
// RSVPs, organizer sessions, and orders locally.
//
// The clock is injected via now() for deterministic tests.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore opens (or creates) the SQLite database at dsn, runs migrations,
// and returns a Store. If dsn is empty, DefaultDSN is used.
func NewStore(dsn string) (*Store, error) {
	if dsn == "" {
		dsn = DefaultDSN
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("web: open sqlite: %w", err)
	}
	// SQLite prefers a single connection for writes in WAL mode.
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("web: migrate: %w", err)
	}
	return s, nil
}

// SetClock overrides the time source. Test-only.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// Close closes the underlying database handle.
func (s *Store) Close() error { return s.db.Close() }

// migrate creates the schema if it does not already exist.
func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS groups_cache (
			group_key      TEXT PRIMARY KEY,
			canonical_name TEXT NOT NULL DEFAULT '',
			display_name   TEXT NOT NULL DEFAULT '',
			description    TEXT NOT NULL DEFAULT '',
			cached_at      INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS events_cache (
			group_key   TEXT NOT NULL DEFAULT '',
			event_id    TEXT NOT NULL DEFAULT '',
			title       TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL DEFAULT '',
			starts_at   INTEGER NOT NULL DEFAULT 0,
			location    TEXT NOT NULL DEFAULT '',
			capacity    INTEGER NOT NULL DEFAULT 0,
			cancelled   INTEGER NOT NULL DEFAULT 0,
			cached_at   INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (group_key, event_id)
		)`,
		`CREATE TABLE IF NOT EXISTS rsvps (
			group_key  TEXT NOT NULL DEFAULT '',
			event_id   TEXT NOT NULL DEFAULT '',
			user_email TEXT NOT NULL DEFAULT '',
			user_name  TEXT NOT NULL DEFAULT '',
			token      TEXT NOT NULL DEFAULT '',
			confirmed  INTEGER NOT NULL DEFAULT 0,
			attended   INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (group_key, event_id, user_email)
		)`,
		`CREATE TABLE IF NOT EXISTS organizer_sessions (
			token      TEXT PRIMARY KEY,
			group_key  TEXT NOT NULL DEFAULT '',
			expires_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS orders (
			order_id         TEXT PRIMARY KEY,
			group_key        TEXT NOT NULL DEFAULT '',
			event_id         TEXT NOT NULL DEFAULT '',
			email            TEXT NOT NULL DEFAULT '',
			amount_cents     INTEGER NOT NULL DEFAULT 0,
			status           TEXT NOT NULL DEFAULT '',
			stripe_session_id TEXT NOT NULL DEFAULT '',
			created_at       INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_start ON events_cache(starts_at)`,
		`CREATE INDEX IF NOT EXISTS idx_events_group ON events_cache(group_key)`,
		`CREATE INDEX IF NOT EXISTS idx_rsvps_event ON rsvps(event_id)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt, err)
		}
	}
	return nil
}

// ---- Group cache ----

// CachedGroup is a group cached in the local SQLite store.
type CachedGroup struct {
	GroupKey      string
	CanonicalName string
	DisplayName   string
	Description   string
}

// UpsertGroup inserts or updates a group in the cache.
func (s *Store) UpsertGroup(g CachedGroup) error {
	_, err := s.db.Exec(
		`INSERT INTO groups_cache (group_key, canonical_name, display_name, description, cached_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(group_key) DO UPDATE SET
		   canonical_name=excluded.canonical_name,
		   display_name=excluded.display_name,
		   description=excluded.description,
		   cached_at=excluded.cached_at`,
		g.GroupKey, g.CanonicalName, g.DisplayName, g.Description, s.now().Unix(),
	)
	return err
}

// ListGroups returns all cached groups, sorted by display_name.
func (s *Store) ListGroups() ([]CachedGroup, error) {
	rows, err := s.db.Query(
		`SELECT group_key, canonical_name, display_name, description FROM groups_cache ORDER BY display_name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CachedGroup
	for rows.Next() {
		var g CachedGroup
		if err := rows.Scan(&g.GroupKey, &g.CanonicalName, &g.DisplayName, &g.Description); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGroupByCanonicalName returns a group by its canonical name.
func (s *Store) GetGroupByCanonicalName(name string) (CachedGroup, error) {
	var g CachedGroup
	err := s.db.QueryRow(
		`SELECT group_key, canonical_name, display_name, description FROM groups_cache WHERE canonical_name = ?`,
		name,
	).Scan(&g.GroupKey, &g.CanonicalName, &g.DisplayName, &g.Description)
	if err != nil {
		return CachedGroup{}, err
	}
	return g, nil
}

// GetGroup returns a group by its group_key.
func (s *Store) GetGroup(groupKey string) (CachedGroup, error) {
	var g CachedGroup
	err := s.db.QueryRow(
		`SELECT group_key, canonical_name, display_name, description FROM groups_cache WHERE group_key = ?`,
		groupKey,
	).Scan(&g.GroupKey, &g.CanonicalName, &g.DisplayName, &g.Description)
	if err != nil {
		return CachedGroup{}, err
	}
	return g, nil
}

// DeleteGroup removes a group from the cache.
func (s *Store) DeleteGroup(groupKey string) error {
	_, err := s.db.Exec(`DELETE FROM groups_cache WHERE group_key = ?`, groupKey)
	return err
}

// ---- Event cache ----

// CachedEvent is an event cached in the local SQLite store.
type CachedEvent struct {
	GroupKey    string
	EventID     string
	Title       string
	Description string
	StartsAt    int64 // unix timestamp
	Location    string
	Capacity    int
	Cancelled   bool
}

// UpsertEvent inserts or updates an event in the cache.
func (s *Store) UpsertEvent(e CachedEvent) error {
	cancelled := 0
	if e.Cancelled {
		cancelled = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO events_cache (group_key, event_id, title, description, starts_at, location, capacity, cancelled, cached_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(group_key, event_id) DO UPDATE SET
		   title=excluded.title,
		   description=excluded.description,
		   starts_at=excluded.starts_at,
		   location=excluded.location,
		   capacity=excluded.capacity,
		   cancelled=excluded.cancelled,
		   cached_at=excluded.cached_at`,
		e.GroupKey, e.EventID, e.Title, e.Description, e.StartsAt, e.Location, e.Capacity, cancelled, s.now().Unix(),
	)
	return err
}

// GetEvent returns a cached event by group_key + event_id.
func (s *Store) GetEvent(groupKey, eventID string) (CachedEvent, error) {
	var e CachedEvent
	var cancelled int
	err := s.db.QueryRow(
		`SELECT group_key, event_id, title, description, starts_at, location, capacity, cancelled FROM events_cache WHERE group_key = ? AND event_id = ?`,
		groupKey, eventID,
	).Scan(&e.GroupKey, &e.EventID, &e.Title, &e.Description, &e.StartsAt, &e.Location, &e.Capacity, &cancelled)
	if err != nil {
		return CachedEvent{}, err
	}
	e.Cancelled = cancelled != 0
	return e, nil
}

// ListUpcomingEvents returns upcoming (non-cancelled, starts_at >= now) events,
// optionally filtered by group_key (empty = all groups), sorted by starts_at.
func (s *Store) ListUpcomingEvents(groupKey string, limit int) ([]CachedEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	now := s.now().Unix()
	var rows *sql.Rows
	var err error
	if groupKey != "" {
		rows, err = s.db.Query(
			`SELECT group_key, event_id, title, description, starts_at, location, capacity, cancelled
			 FROM events_cache
			 WHERE group_key = ? AND cancelled = 0 AND starts_at >= ?
			 ORDER BY starts_at ASC LIMIT ?`,
			groupKey, now, limit,
		)
	} else {
		rows, err = s.db.Query(
			`SELECT group_key, event_id, title, description, starts_at, location, capacity, cancelled
			 FROM events_cache
			 WHERE cancelled = 0 AND starts_at >= ?
			 ORDER BY starts_at ASC LIMIT ?`,
			now, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CachedEvent
	for rows.Next() {
		var e CachedEvent
		var cancelled int
		if err := rows.Scan(&e.GroupKey, &e.EventID, &e.Title, &e.Description, &e.StartsAt, &e.Location, &e.Capacity, &cancelled); err != nil {
			return nil, err
		}
		e.Cancelled = cancelled != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListEventsByGroup returns all events for a group (including past), sorted by starts_at descending.
func (s *Store) ListEventsByGroup(groupKey string) ([]CachedEvent, error) {
	rows, err := s.db.Query(
		`SELECT group_key, event_id, title, description, starts_at, location, capacity, cancelled
		 FROM events_cache
		 WHERE group_key = ?
		 ORDER BY starts_at DESC`,
		groupKey,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CachedEvent
	for rows.Next() {
		var e CachedEvent
		var cancelled int
		if err := rows.Scan(&e.GroupKey, &e.EventID, &e.Title, &e.Description, &e.StartsAt, &e.Location, &e.Capacity, &cancelled); err != nil {
			return nil, err
		}
		e.Cancelled = cancelled != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteEvent removes an event from the cache.
func (s *Store) DeleteEvent(groupKey, eventID string) error {
	_, err := s.db.Exec(`DELETE FROM events_cache WHERE group_key = ? AND event_id = ?`, groupKey, eventID)
	return err
}

// ---- RSVPs ----

// RSVPRecord is a single RSVP stored locally.
type RSVPRecord struct {
	GroupKey  string
	EventID   string
	UserEmail string
	UserName  string
	Token     string
	Confirmed bool
	Attended  bool
	CreatedAt int64
}

// CreateRsvp inserts a new RSVP with a magic-link token. Returns an error if
// an RSVP for the same (group_key, event_id, email) already exists.
func (s *Store) CreateRsvp(r RSVPRecord) error {
	confirmed := 0
	if r.Confirmed {
		confirmed = 1
	}
	attended := 0
	if r.Attended {
		attended = 1
	}
	if r.CreatedAt == 0 {
		r.CreatedAt = s.now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO rsvps (group_key, event_id, user_email, user_name, token, confirmed, attended, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(group_key, event_id, user_email) DO UPDATE SET
		   user_name=excluded.user_name,
		   token=excluded.token,
		   confirmed=excluded.confirmed,
		   attended=excluded.attended`,
		r.GroupKey, r.EventID, r.UserEmail, r.UserName, r.Token, confirmed, attended, r.CreatedAt,
	)
	return err
}

// ConfirmRsvp marks the RSVP matching the token as confirmed.
// Returns the RSVP record if found.
func (s *Store) ConfirmRsvp(token string) (RSVPRecord, error) {
	var r RSVPRecord
	var confirmed int
	var attended int
	err := s.db.QueryRow(
		`SELECT group_key, event_id, user_email, user_name, token, confirmed, attended, created_at FROM rsvps WHERE token = ?`,
		token,
	).Scan(&r.GroupKey, &r.EventID, &r.UserEmail, &r.UserName, &r.Token, &confirmed, &attended, &r.CreatedAt)
	if err != nil {
		return RSVPRecord{}, err
	}
	r.Confirmed = confirmed != 0
	r.Attended = attended != 0
	if r.Confirmed {
		return r, nil // already confirmed — idempotent
	}
	_, err = s.db.Exec(`UPDATE rsvps SET confirmed = 1 WHERE token = ?`, token)
	if err != nil {
		return RSVPRecord{}, err
	}
	r.Confirmed = true
	return r, nil
}

// GetRsvpByToken returns the RSVP matching the token.
func (s *Store) GetRsvpByToken(token string) (RSVPRecord, error) {
	var r RSVPRecord
	var confirmed int
	var attended int
	err := s.db.QueryRow(
		`SELECT group_key, event_id, user_email, user_name, token, confirmed, attended, created_at FROM rsvps WHERE token = ?`,
		token,
	).Scan(&r.GroupKey, &r.EventID, &r.UserEmail, &r.UserName, &r.Token, &confirmed, &attended, &r.CreatedAt)
	if err != nil {
		return RSVPRecord{}, err
	}
	r.Confirmed = confirmed != 0
	r.Attended = attended != 0
	return r, nil
}

// RsvpCount returns the number of confirmed RSVPs for an event.
func (s *Store) RsvpCount(groupKey, eventID string) (int, error) {
	var count int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM rsvps WHERE group_key = ? AND event_id = ? AND confirmed = 1`,
		groupKey, eventID,
	).Scan(&count)
	return count, err
}

// ListRsvpsForEvent returns all confirmed RSVPs for an event.
func (s *Store) ListRsvpsForEvent(groupKey, eventID string) ([]RSVPRecord, error) {
	rows, err := s.db.Query(
		`SELECT group_key, event_id, user_email, user_name, token, confirmed, attended, created_at FROM rsvps
		 WHERE group_key = ? AND event_id = ? AND confirmed = 1 ORDER BY created_at ASC`,
		groupKey, eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RSVPRecord
	for rows.Next() {
		var r RSVPRecord
		var confirmed int
		var attended int
		if err := rows.Scan(&r.GroupKey, &r.EventID, &r.UserEmail, &r.UserName, &r.Token, &confirmed, &attended, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Confirmed = confirmed != 0
		r.Attended = attended != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListRsvpsByEmail returns all RSVPs (confirmed and unconfirmed) for a given email.
func (s *Store) ListRsvpsByEmail(email string) ([]RSVPRecord, error) {
	rows, err := s.db.Query(
		`SELECT group_key, event_id, user_email, user_name, token, confirmed, attended, created_at FROM rsvps
		 WHERE user_email = ? ORDER BY created_at DESC`,
		email,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RSVPRecord
	for rows.Next() {
		var r RSVPRecord
		var confirmed int
		var attended int
		if err := rows.Scan(&r.GroupKey, &r.EventID, &r.UserEmail, &r.UserName, &r.Token, &confirmed, &attended, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Confirmed = confirmed != 0
		r.Attended = attended != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// CancelRsvp sets the RSVP status to unconfirmed (effectively canceling it).
func (s *Store) CancelRsvp(groupKey, eventID, email string) error {
	_, err := s.db.Exec(
		`UPDATE rsvps SET confirmed = 0 WHERE group_key = ? AND event_id = ? AND user_email = ?`,
		groupKey, eventID, email,
	)
	return err
}

// MarkRsvpAttended marks an RSVP as attended.
func (s *Store) MarkRsvpAttended(eventID, email string) error {
	_, err := s.db.Exec(
		`UPDATE rsvps SET attended = 1 WHERE event_id = ? AND user_email = ?`,
		eventID, email,
	)
	return err
}

// ---- Organizer sessions ----

// CreateSession inserts a new organizer session token.
func (s *Store) CreateSession(token, groupKey string, ttl time.Duration) error {
	expiresAt := s.now().Add(ttl).Unix()
	_, err := s.db.Exec(
		`INSERT INTO organizer_sessions (token, group_key, expires_at) VALUES (?, ?, ?)
		 ON CONFLICT(token) DO UPDATE SET group_key=excluded.group_key, expires_at=excluded.expires_at`,
		token, groupKey, expiresAt,
	)
	return err
}

// ValidateSession checks if a session token is valid and not expired.
// Returns the group_key if valid.
func (s *Store) ValidateSession(token string) (string, bool) {
	var groupKey string
	var expiresAt int64
	err := s.db.QueryRow(
		`SELECT group_key, expires_at FROM organizer_sessions WHERE token = ?`,
		token,
	).Scan(&groupKey, &expiresAt)
	if err != nil {
		return "", false
	}
	if s.now().Unix() > expiresAt {
		return "", false
	}
	return groupKey, true
}

// DeleteSession removes a session token.
func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM organizer_sessions WHERE token = ?`, token)
	return err
}

// ---- Orders ----

// OrderRecord is a local order record.
type OrderRecord struct {
	OrderID         string
	GroupKey        string
	EventID         string
	Email           string
	AmountCents     int
	Status          string
	StripeSessionID string
	CreatedAt       int64
}

// CreateOrder inserts a new order.
func (s *Store) CreateOrder(o OrderRecord) error {
	if o.CreatedAt == 0 {
		o.CreatedAt = s.now().Unix()
	}
	_, err := s.db.Exec(
		`INSERT INTO orders (order_id, group_key, event_id, email, amount_cents, status, stripe_session_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(order_id) DO UPDATE SET
		   status=excluded.status,
		   stripe_session_id=excluded.stripe_session_id`,
		o.OrderID, o.GroupKey, o.EventID, o.Email, o.AmountCents, o.Status, o.StripeSessionID, o.CreatedAt,
	)
	return err
}

// GetOrder returns an order by order_id.
func (s *Store) GetOrder(orderID string) (OrderRecord, error) {
	var o OrderRecord
	err := s.db.QueryRow(
		`SELECT order_id, group_key, event_id, email, amount_cents, status, stripe_session_id, created_at FROM orders WHERE order_id = ?`,
		orderID,
	).Scan(&o.OrderID, &o.GroupKey, &o.EventID, &o.Email, &o.AmountCents, &o.Status, &o.StripeSessionID, &o.CreatedAt)
	if err != nil {
		return OrderRecord{}, err
	}
	return o, nil
}

// ListOrdersByEvent returns all orders for an event.
func (s *Store) ListOrdersByEvent(groupKey, eventID string) ([]OrderRecord, error) {
	rows, err := s.db.Query(
		`SELECT order_id, group_key, event_id, email, amount_cents, status, stripe_session_id, created_at
		 FROM orders WHERE group_key = ? AND event_id = ? ORDER BY created_at DESC`,
		groupKey, eventID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrderRecord
	for rows.Next() {
		var o OrderRecord
		if err := rows.Scan(&o.OrderID, &o.GroupKey, &o.EventID, &o.Email, &o.AmountCents, &o.Status, &o.StripeSessionID, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpdateOrderStatus updates the status of an order.
func (s *Store) UpdateOrderStatus(orderID, status string) error {
	_, err := s.db.Exec(`UPDATE orders SET status = ? WHERE order_id = ?`, status, orderID)
	return err
}

// ---- Utility ----

// HexKey converts a 32-byte public key to a hex string for use as group_key.
func HexKey(key []byte) string {
	return hex.EncodeToString(key)
}