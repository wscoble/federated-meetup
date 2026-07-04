// SPDX-License-Identifier: AGPL-3.0
//
// Persistence for the protocol state machine.
//
// The group state machine (State) is fundamentally an append-only log
// of signed transitions applied to a Merkle KV tree. If we persist
// every transition to an append-only SQLite log, we can replay them
// all on startup and reconstruct the exact same state — the state
// root is deterministic.
//
// This file defines:
//   - Persister: an interface for saving/loading transitions.
//   - SQLitePersister: a SQLite-backed implementation using
//     modernc.org/sqlite (pure-Go, no CGO).
//
// The persister is opt-in. If nil (the default), State is entirely
// in-memory and behavior is unchanged from pre-persistence versions.
//
// Schema:
//   CREATE TABLE IF NOT EXISTS transitions (
//       seq            INTEGER PRIMARY KEY,
//       canonical_bytes BLOB NOT NULL,
//       transition     BLOB NOT NULL
//   );
//
// seq is the append order (0-based). canonical_bytes is the
// deterministic protobuf encoding of the transition (without
// signatures), used for verification. transition is the full
// protobuf encoding (with signatures).

package group

import (
	"context"
	"database/sql"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/wscoble/federated-meetup/internal/types"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"

	_ "modernc.org/sqlite"
)

// Persister is the interface for persisting and loading transitions.
// Implementations must be safe for concurrent use.
//
// If a State is constructed with a non-nil Persister:
//   - On construction, LoadTransitions is called and all returned
//     transitions are replayed via Apply (rebuilding the state).
//   - On each successful Apply, SaveTransition is called to append
//     the transition to the durable log.
//
// If the Persister is nil, State is entirely in-memory.
type Persister interface {
	// SaveTransition appends a transition to the durable log.
	// It is called after a successful Apply, so the transition
	// is already part of the in-memory state. If this returns an
	// error, the in-memory state has already advanced — the caller
	// should treat this as a fatal error (the log is now behind
	// the state).
	SaveTransition(ctx context.Context, t *Transition) error

	// LoadTransitions reads all transitions from the durable log
	// in append order (seq ascending). Used during replay on
	// startup.
	LoadTransitions(ctx context.Context) ([]*Transition, error)

	// Close releases any resources held by the persister.
	Close() error
}

// SQLitePersister is a SQLite-backed Persister using modernc.org/sqlite
// (pure-Go, no CGO). The database lives at the given DSN.
//
// The transitions table is an append-only log. Each row stores the
// full protobuf-encoded transition (with signatures) and the canonical
// sign-bytes (for fast verification on replay).
type SQLitePersister struct {
	db  *sql.DB
	gid types.GroupID
}

// NewSQLitePersister opens (or creates) the SQLite database at dsn,
// runs the schema migration, and returns a persister bound to the
// given group ID. The group ID is used to reconstruct Transition
// objects on LoadTransitions (the proto does not carry the group ID).
func NewSQLitePersister(dsn string, gid GroupID) (*SQLitePersister, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("group/persistence: open sqlite: %w", err)
	}
	// Single connection for write safety in WAL mode.
	db.SetMaxOpenConns(1)
	p := &SQLitePersister{db: db, gid: gid}
	if err := p.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("group/persistence: migrate: %w", err)
	}
	return p, nil
}

// migrate creates the schema if it does not already exist.
func (p *SQLitePersister) migrate() error {
	_, err := p.db.Exec(`CREATE TABLE IF NOT EXISTS transitions (
		seq             INTEGER PRIMARY KEY,
		canonical_bytes BLOB NOT NULL,
		transition      BLOB NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}
	_, err = p.db.Exec(`CREATE INDEX IF NOT EXISTS idx_transitions_seq ON transitions(seq)`)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}
	return nil
}

// SaveTransition appends a transition to the durable log.
func (p *SQLitePersister) SaveTransition(ctx context.Context, t *Transition) error {
	// Marshal the full protobuf transition (with signatures).
	transitionBytes, err := proto.Marshal(t.Proto)
	if err != nil {
		return fmt.Errorf("group/persistence: marshal transition: %w", err)
	}
	_, err = p.db.ExecContext(ctx,
		`INSERT INTO transitions (canonical_bytes, transition) VALUES (?, ?)`,
		t.canonical, transitionBytes,
	)
	if err != nil {
		return fmt.Errorf("group/persistence: insert transition: %w", err)
	}
	return nil
}

// LoadTransitions reads all transitions from the durable log in
// append order (seq ascending).
func (p *SQLitePersister) LoadTransitions(ctx context.Context) ([]*Transition, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT canonical_bytes, transition FROM transitions ORDER BY seq ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("group/persistence: query transitions: %w", err)
	}
	defer rows.Close()

	var out []*Transition
	for rows.Next() {
		var canonicalBytes, transitionBytes []byte
		if err := rows.Scan(&canonicalBytes, &transitionBytes); err != nil {
			return nil, fmt.Errorf("group/persistence: scan: %w", err)
		}
		var pbT pb.Transition
		if err := proto.Unmarshal(transitionBytes, &pbT); err != nil {
			return nil, fmt.Errorf("group/persistence: unmarshal transition: %w", err)
		}
		t, err := NewTransition(&pbT, p.gid)
		if err != nil {
			return nil, fmt.Errorf("group/persistence: reconstruct transition: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Close closes the underlying database handle.
func (p *SQLitePersister) Close() error {
	if p.db == nil {
		return nil
	}
	return p.db.Close()
}