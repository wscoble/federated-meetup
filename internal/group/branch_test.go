// SPDX-License-Identifier: MIT
//
// Branch-local mutation tests (post-2026-06-27 refactor).
//
// Each branch is an independent state machine. Mutations on branch N
// do not affect branch M's KV, log, equivocation log, or steward
// history. Cross-branch "equivocation" is meaningless (different
// state machines).

package group

import (
	"bytes"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/hlc"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// typesGroupID is a tiny helper to keep the test file self-contained.
func typesGroupID(b byte) types.GroupID {
	var g types.GroupID
	g[0] = b
	return g
}

func bytesEqualHelper(a, b []byte) bool {
	return bytes.Equal(a, b)
}

// TestBranch_GenesisOnly tests that CREATE_GROUP creates branch 0
// and only branch 0.
func TestBranch_GenesisOnly(t *testing.T) {
	gid := typesGroupID(0x70)
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	if got := st.BranchCount(); got != 1 {
		t.Errorf("expected 1 branch after CREATE_GROUP, got %d", got)
	}
	branches := st.Branches()
	if len(branches) != 1 {
		t.Fatalf("Branches() returned %d entries, want 1", len(branches))
	}
	if branches[0].BranchId != 0 {
		t.Errorf("expected branch 0, got %d", branches[0].BranchId)
	}
}

// TestBranch_CreateAllocatesNewBranch verifies that BRANCH_CREATE
// allocates a new branch inheriting the parent's stewards.
func TestBranch_CreateAllocatesNewBranch(t *testing.T) {
	gid := typesGroupID(0x71)
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		Payload: &pb.Transition_BranchCreate{
			BranchCreate: &pb.BranchCreatePayload{
				Reason: "steward dispute over event location policy",
			},
		},
		Hlc: hlc.New(time.Now()),
	}
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)

	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
		t.Fatalf("BRANCH_CREATE apply: %v", err)
	}

	if got := st.BranchCount(); got != 2 {
		t.Errorf("expected 2 branches after BRANCH_CREATE, got %d", got)
	}
	branches := st.Branches()
	if len(branches) != 2 {
		t.Fatalf("Branches() returned %d entries, want 2", len(branches))
	}
	// Branch 1 (newly created) should have:
	//   - branch_id 1
	//   - parent_branch_id 0
	//   - reason matching the payload
	//   - initial stewards inherited from branch 0
	newB := branches[1]
	if newB.BranchId != 1 {
		t.Errorf("expected new branch id 1, got %d", newB.BranchId)
	}
	if newB.ParentBranchId != 0 {
		t.Errorf("expected parent branch 0, got %d", newB.ParentBranchId)
	}
	if newB.Reason != "steward dispute over event location policy" {
		t.Errorf("reason not preserved: %q", newB.Reason)
	}
	// Verify the new branch inherited the stewards.
	newBranch := st.Branch(1)
	if newBranch == nil {
		t.Fatal("Branch(1) returned nil")
	}
	if newBranch.parentBranchID != GenesisBranchID {
		t.Errorf("new branch parent = %d, want 0", newBranch.parentBranchID)
	}
	if len(newBranch.initialStewards) != 3 {
		t.Errorf("new branch has %d initial stewards, want 3 (inherited)", len(newBranch.initialStewards))
	}
	if newBranch.initialThreshold != 2 {
		t.Errorf("new branch threshold = %d, want 2 (inherited)", newBranch.initialThreshold)
	}
}

// TestBranch_BranchCapEnforced verifies that MaxBranches is honored.
func TestBranch_BranchCapEnforced(t *testing.T) {
	gid := typesGroupID(0x72)
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)
	st.MaxBranches = 2 // cap at 2 (branch 0 + 1 new = 2 max)

	// First BRANCH_CREATE succeeds (we have branch 0; cap=2 allows 1 more).
	tr1 := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		Payload: &pb.Transition_BranchCreate{
			BranchCreate: &pb.BranchCreatePayload{Reason: "first branch"},
		},
		Hlc: hlc.New(time.Now()),
	}
	tr1.PriorState = stateRootFromHead(st)
	signTransition(tr1, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr1, gid), time.Now()); err != nil {
		t.Fatalf("first BRANCH_CREATE: %v", err)
	}

	// Second BRANCH_CREATE: would exceed cap.
	tr2 := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		Payload: &pb.Transition_BranchCreate{
			BranchCreate: &pb.BranchCreatePayload{Reason: "second branch"},
		},
		Hlc: hlc.New(time.Now()),
	}
	tr2.PriorState = stateRootFromHead(st)
	signTransition(tr2, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr2, gid), time.Now()); err == nil {
		t.Fatalf("second BRANCH_CREATE should have been rejected by MaxBranches cap")
	}
}

// TestBranch_TransitionMustReferenceExistingBranch verifies that
// transitions targeting non-genesis non-existent branches fail.
func TestBranch_TransitionMustReferenceExistingBranch(t *testing.T) {
	gid := typesGroupID(0x73)
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	// Try to apply a transition with branch_id=42 (doesn't exist).
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		Payload: &pb.Transition_CreateEvent{
			CreateEvent: &pb.CreateEventPayload{
				EventId: "phantom",
				Title:   "Ghost event",
			},
		},
		BranchId: 42,
		Hlc:      hlc.New(time.Now()),
	}
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err == nil {
		t.Fatalf("transition to non-existent branch should have been rejected")
	}
}

// TestBranch_GenesisHLCRecorded verifies that the new branch
// records its genesis HLC correctly.
func TestBranch_GenesisHLCRecorded(t *testing.T) {
	gid := typesGroupID(0x74)
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	hlcBytes := hlc.New(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		Payload: &pb.Transition_BranchCreate{
			BranchCreate: &pb.BranchCreatePayload{Reason: "test genesis HLC"},
		},
		Hlc: hlcBytes,
	}
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
		t.Fatalf("BRANCH_CREATE: %v", err)
	}

	newBranch := st.Branch(1)
	if newBranch == nil {
		t.Fatal("Branch(1) returned nil")
	}
	got := newBranch.GenesisHLC()
	if !bytesEqualHelper(got, hlcBytes) {
		t.Errorf("genesis HLC = %x, want %x", got, hlcBytes)
	}
}

// End of tests. Helpers (typesGroupID, bytesEqualHelper) defined
// at top of file.