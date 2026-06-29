// SPDX-License-Identifier: MIT
//
// Branch inheritance semantics. BRANCH_CREATE (internal/group/group.go:793)
// captures the parent's current stewards and threshold AT THE MOMENT OF
// CREATION and snapshots them into the new branch:
//
//     parentStewards := s.stewardsAtLocked(t.Proto.GetPriorState())
//     parentThreshold := s.thresholdAtLocked(t.Proto.GetPriorState())
//     newBranch.initialStewards = append([]Steward(nil), parentStewards...)
//     newBranch.initialThreshold = parentThreshold
//
// This is a SNAPSHOT, not a live link. Subsequent changes to the parent
// (ADD_STEWARD, CHANGE_THRESHOLD, REMOVE_STEWARD, etc.) do NOT propagate
// to the child branch.
//
// Why this matters:
//
//   1. Branch isolation is the protocol's only mechanism for soft
//      in-group disagreement. If child branches inherited live state
//      from the parent, every parent's CHANGE_THRESHOLD would surprise
//      every child — turning branches into footguns.
//
//   2. Federation divergence: hosts must agree on which steward set
//      governs a branch. A snapshot at BRANCH_CREATE is a deterministic
//      anchor — every host that replays the same transition reconstructs
//      the same child branch with the same initial stewards.
//
//   3. Branch-local mutations are NOT yet wired (cycle 32 finding).
//      Combined with snapshot inheritance, this means branches are
//      effectively read-only metadata right now — they exist for
//      bookkeeping, not for divergent evolution. That's a known
//      limitation, not a bug.
//
// What this test pins down:
//
//   - At BRANCH_CREATE time, child.initialStewards == parent.StewardsAt(prior)
//   - At BRANCH_CREATE time, child.initialThreshold == parent.ThresholdAt(prior)
//   - Subsequent ADD_STEWARD on parent does NOT change child stewards
//   - Subsequent CHANGE_THRESHOLD on parent does NOT change child threshold
//
// If any of these regress (e.g., a future engineer "fixes" branch
// inheritance to be live-linked), this test fails loudly.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBranch_InheritsParentStewardsAtCreation(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        96,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	// Pre-branch: Vegas Programmers has [alice, bob, carol] (3 stewards),
	// threshold 2. setupVegasProgrammers creates this state.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	// Capture parent's stewards/threshold at the moment of branch creation.
	parentStewardsAtBranch := w.Hosts()[0].State(gkp.Public).Stewards()
	parentThresholdAtBranch := w.Hosts()[0].State(gkp.Public).Threshold()
	if len(parentStewardsAtBranch) != 3 {
		t.Fatalf("expected 3 stewards at branch time, got %d", len(parentStewardsAtBranch))
	}
	if parentThresholdAtBranch != 2 {
		t.Fatalf("expected threshold 2 at branch time, got %d", parentThresholdAtBranch)
	}

	// BRANCH_CREATE.
	branchProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload: &pb.Transition_BranchCreate{BranchCreate: &pb.BranchCreatePayload{
			Reason: "isolated-steward-test",
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canon, err := group.MarshalCanonicalForSigningHelper(branchProto)
	if err != nil {
		t.Fatal(err)
	}
	sigs := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canon)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	branchProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, err := group.NewTransition(branchProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, tx); err != nil {
			t.Fatalf("BRANCH_CREATE on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)

	// Verify the child branch (branch 1) inherited the parent stewards
	// and threshold at the snapshot.
	childBranch := w.Hosts()[0].State(gkp.Public).Branch(group.BranchID(1))
	if childBranch == nil {
		t.Fatal("branch 1 not allocated")
	}
	if got := childBranch.InitialStewards(); len(got) != 3 {
		t.Errorf("branch 1 initialStewards count = %d, want 3", len(got))
	}
	if got := childBranch.InitialThreshold(); got != 2 {
		t.Errorf("branch 1 initialThreshold = %d, want 2", got)
	}
	t.Logf("branch 1 inherited %d stewards, threshold %d at creation",
		len(childBranch.InitialStewards()), childBranch.InitialThreshold())
}

func TestBranch_DoesNotInheritParentChanges(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        97,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	// Step 1: BRANCH_CREATE while parent has 3 stewards, threshold 2.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	branchProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload: &pb.Transition_BranchCreate{BranchCreate: &pb.BranchCreatePayload{
			Reason: "isolation-test",
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canon, _ := group.MarshalCanonicalForSigningHelper(branchProto)
	sigs := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canon)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	branchProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, _ := group.NewTransition(branchProto, gkp.Public)
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, tx); err != nil {
			t.Fatalf("BRANCH_CREATE: %v", err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterBranch := w.Hosts()[0].State(gkp.Public).Root()

	// Snapshot the child branch's state for comparison later.
	childStewards := w.Hosts()[0].State(gkp.Public).Branch(group.BranchID(1)).InitialStewards()
	childThreshold := w.Hosts()[0].State(gkp.Public).Branch(group.BranchID(1)).InitialThreshold()

	// Step 2: ADD_STEWARD on parent (brings stewards to 4).
	newKP, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		PriorState: &pb.StateRoot{Hash: rootAfterBranch[:]},
		Payload: &pb.Transition_AddSteward{AddSteward: &pb.AddStewardPayload{
			NewSteward: &pb.PublicKey{Raw: newKP.Public[:]},
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canon2, _ := group.MarshalCanonicalForSigningHelper(addProto)
	sigs2 := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canon2)
		sigs2 = append(sigs2, &pb.Signature{Raw: s[:]})
	}
	addProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs2}
	tx2, _ := group.NewTransition(addProto, gkp.Public)
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, tx2); err != nil {
			t.Fatalf("ADD_STEWARD: %v", err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterAdd := w.Hosts()[0].State(gkp.Public).Root()

	// Verify parent has 4 stewards now.
	if got := len(w.Hosts()[0].State(gkp.Public).Stewards()); got != 4 {
		t.Fatalf("parent stewards = %d after ADD_STEWARD, want 4", got)
	}

	// Verify child branch STILL has 3 stewards (snapshot inheritance).
	gotChild := w.Hosts()[0].State(gkp.Public).Branch(group.BranchID(1))
	if len(gotChild.InitialStewards()) != len(childStewards) {
		t.Fatalf("child branch initialStewards count changed: was %d, now %d",
			len(childStewards), len(gotChild.InitialStewards()))
	}

	// Step 3: CHANGE_THRESHOLD on parent (3 → 1).
	changeProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		PriorState: &pb.StateRoot{Hash: rootAfterAdd[:]},
		Payload: &pb.Transition_ChangeThreshold{ChangeThreshold: &pb.ChangeThresholdPayload{
			NewThreshold: 1,
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canon3, _ := group.MarshalCanonicalForSigningHelper(changeProto)
	sigs3 := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canon3)
		sigs3 = append(sigs3, &pb.Signature{Raw: s[:]})
	}
	changeProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs3}
	tx3, _ := group.NewTransition(changeProto, gkp.Public)
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, tx3); err != nil {
			t.Fatalf("CHANGE_THRESHOLD: %v", err)
		}
	}
	w.Advance(50 * time.Millisecond)

	// Verify parent threshold is now 1.
	if got := w.Hosts()[0].State(gkp.Public).Threshold(); got != 1 {
		t.Fatalf("parent threshold = %d after CHANGE_THRESHOLD, want 1", got)
	}

	// Verify child branch threshold UNCHANGED (snapshot inheritance).
	gotChild2 := w.Hosts()[0].State(gkp.Public).Branch(group.BranchID(1))
	if gotChild2.InitialThreshold() != childThreshold {
		t.Fatalf("child branch initialThreshold changed: was %d, now %d",
			childThreshold, gotChild2.InitialThreshold())
	}

	// All 4 hosts must agree on the child branch's snapshot.
	for _, h := range w.Hosts()[1:] {
		hostChild := h.State(gkp.Public).Branch(group.BranchID(1))
		if len(hostChild.InitialStewards()) != 3 {
			t.Errorf("host %s child branch has %d initial stewards, want 3",
				h.ID(), len(hostChild.InitialStewards()))
		}
		if hostChild.InitialThreshold() != 2 {
			t.Errorf("host %s child branch threshold = %d, want 2",
				h.ID(), hostChild.InitialThreshold())
		}
	}
	t.Logf("branch isolation confirmed: child has 3 stewards, threshold 2; parent has 4 stewards, threshold 1")
}