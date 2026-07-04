// SPDX-License-Identifier: AGPL-3.0
//
// Persistence tests for the SQLite transition log.

package group

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/types"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
)

// TestSQLitePersister_SaveLoad verifies that a transition saved to
// SQLite can be loaded back and reconstructs the same canonical bytes
// and group ID.
func TestSQLitePersister_SaveLoad(t *testing.T) {
	gid := types.GroupID{0xAA}
	dsn := "file::memory:?cache=shared"
	p, err := NewSQLitePersister(dsn, gid)
	if err != nil {
		t.Fatalf("NewSQLitePersister: %v", err)
	}
	defer p.Close()

	// Build a CREATE_GROUP transition.
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	initialPubs := make([]*pb.PublicKey, len(stewards))
	for i, k := range stewards {
		initialPubs[i] = pub(k)
	}
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload: &pb.Transition_CreateGroup{
			CreateGroup: &pb.CreateGroupPayload{
				CanonicalName:   "test-persist",
				DisplayName:     "Test Persist",
				InitialStewards: initialPubs,
				Threshold:       2,
			},
		},
	}
	signTransition(tr, stewards[:2], gid)
	trans, err := NewTransition(tr, gid)
	if err != nil {
		t.Fatalf("NewTransition: %v", err)
	}

	// Save.
	ctx := context.Background()
	if err := p.SaveTransition(ctx, trans); err != nil {
		t.Fatalf("SaveTransition: %v", err)
	}

	// Load.
	loaded, err := p.LoadTransitions(ctx)
	if err != nil {
		t.Fatalf("LoadTransitions: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 transition, got %d", len(loaded))
	}

	// Verify canonical bytes match.
	if string(loaded[0].Canonical()) != string(trans.Canonical()) {
		t.Errorf("canonical bytes mismatch")
	}
	// Verify group ID.
	if loaded[0].GroupID() != gid {
		t.Errorf("group ID mismatch: got %x, want %x", loaded[0].GroupID(), gid)
	}
}

// TestNewStateWithPersister_Replay verifies that creating a State
// with a persister replays all stored transitions and produces the
// same state root as the original.
func TestNewStateWithPersister_Replay(t *testing.T) {
	gid := types.GroupID{0xBB}
	dbPath := filepath.Join(t.TempDir(), "test_protocol.db")
	dsn := "file:" + dbPath + "?_journal_mode=WAL&_busy_timeout=5000"

	// Phase 1: Create state with persister, apply transitions.
	p1, err := NewSQLitePersister(dsn, gid)
	if err != nil {
		t.Fatalf("NewSQLitePersister (phase 1): %v", err)
	}

	st1, err := NewStateWithPersister(gid, p1)
	if err != nil {
		t.Fatalf("NewStateWithPersister (phase 1): %v", err)
	}

	// Apply CREATE_GROUP + a few transitions.
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	initialPubs := make([]*pb.PublicKey, len(stewards))
	for i, k := range stewards {
		initialPubs[i] = pub(k)
	}
	trCreate := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload: &pb.Transition_CreateGroup{
			CreateGroup: &pb.CreateGroupPayload{
				CanonicalName:   "replay-test",
				DisplayName:     "Replay Test",
				InitialStewards: initialPubs,
				Threshold:       2,
			},
		},
	}
	signTransition(trCreate, stewards[:2], gid)
	t1, _ := NewTransition(trCreate, gid)
	if err := st1.Apply(t1, time.Now()); err != nil {
		t.Fatalf("Apply CREATE_GROUP: %v", err)
	}

	// Apply a CREATE_EVENT.
	trEvent := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		Payload: &pb.Transition_CreateEvent{
			CreateEvent: &pb.CreateEventPayload{
				EventId: "event-1",
				Title:   "Test Event",
			},
		},
	}
	stampHLC(trEvent)
	trEvent.PriorState = stateRootFromHead(st1)
	signTransition(trEvent, stewards[:2], gid)
	t2, _ := NewTransition(trEvent, gid)
	if err := st1.Apply(t2, time.Now()); err != nil {
		t.Fatalf("Apply CREATE_EVENT: %v", err)
	}

	root1 := st1.Root()
	count1 := st1.TransitionCount()
	if count1 != 2 {
		t.Fatalf("expected 2 transitions, got %d", count1)
	}

	// Close the persister.
	p1.Close()

	// Phase 2: Reopen the same database and create a new state.
	// The transitions should be replayed.
	p2, err := NewSQLitePersister(dsn, gid)
	if err != nil {
		t.Fatalf("NewSQLitePersister (phase 2): %v", err)
	}
	defer p2.Close()

	st2, err := NewStateWithPersister(gid, p2)
	if err != nil {
		t.Fatalf("NewStateWithPersister (phase 2): %v", err)
	}

	root2 := st2.Root()
	count2 := st2.TransitionCount()

	if count2 != count1 {
		t.Fatalf("transition count mismatch: replayed=%d, original=%d", count2, count1)
	}
	if root2 != root1 {
		t.Fatalf("root mismatch: replayed=0x%x, original=0x%x", root2, root1)
	}

	// Verify the snapshot entries match.
	snap1 := st1.Snapshot()
	snap2 := st2.Snapshot()
	if !snap1.Equal(snap2) {
		t.Fatalf("snapshots differ after replay")
	}
}

// TestNewStateWithPersister_NilPersister verifies that a nil persister
// produces the same behavior as plain NewState (in-memory).
func TestNewStateWithPersister_NilPersister(t *testing.T) {
	gid := types.GroupID{0xCC}
	st, err := NewStateWithPersister(gid, nil)
	if err != nil {
		t.Fatalf("NewStateWithPersister with nil: %v", err)
	}
	if st == nil {
		t.Fatal("state is nil")
	}
	if st.TransitionCount() != 0 {
		t.Errorf("expected 0 transitions, got %d", st.TransitionCount())
	}
}

// TestNewState_BackwardCompat verifies that NewState (without
// persister) still works and does not persist anything.
func TestNewState_BackwardCompat(t *testing.T) {
	gid := types.GroupID{0xDD}
	st := NewState(gid)
	if st == nil {
		t.Fatal("NewState returned nil")
	}
	if st.persister != nil {
		t.Error("persister should be nil for plain NewState")
	}
	if st.replaying {
		t.Error("replaying should be false for plain NewState")
	}
	// Apply a transition — should not panic or error from missing persister.
	stewards := []crypto.KeyPair{genKey(1), genKey(2)}
	initialPubs := make([]*pb.PublicKey, len(stewards))
	for i, k := range stewards {
		initialPubs[i] = pub(k)
	}
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload: &pb.Transition_CreateGroup{
			CreateGroup: &pb.CreateGroupPayload{
				CanonicalName:   "compat-test",
				DisplayName:     "Compat Test",
				InitialStewards: initialPubs,
				Threshold:       1,
			},
		},
	}
	signTransition(tr, stewards[:1], gid)
	trans, _ := NewTransition(tr, gid)
	if err := st.Apply(trans, time.Now()); err != nil {
		t.Fatalf("Apply with nil persister: %v", err)
	}
}

// TestSQLitePersister_EmptyLoad verifies that loading from an empty
// database returns no transitions and no error.
func TestSQLitePersister_EmptyLoad(t *testing.T) {
	gid := types.GroupID{0xEE}
	dsn := "file::memory:?cache=shared&_journal_mode=WAL"
	p, err := NewSQLitePersister(dsn, gid)
	if err != nil {
		t.Fatalf("NewSQLitePersister: %v", err)
	}
	defer p.Close()

	transitions, err := p.LoadTransitions(context.Background())
	if err != nil {
		t.Fatalf("LoadTransitions: %v", err)
	}
	if len(transitions) != 0 {
		t.Fatalf("expected 0 transitions from empty DB, got %d", len(transitions))
	}
}