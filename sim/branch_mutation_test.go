// SPDX-License-Identifier: MIT
//
// Branch-local mutation scenario (C-6 fix). The apply path now
// supports branch-local mutations on non-genesis branches via a
// state-swap pattern: the branch's state is swapped into the legacy
// State fields for the duration of Apply, then swapped back.
//
// What this exercises:
//   - BRANCH_CREATE allocates branch 1 (alongside genesis branch 0)
//   - A subsequent CREATE_EVENT with branch_id=1 SUCCEEDS
//   - The genesis branch is unaffected (no state mutation on branch 0)
//   - The new event exists only on branch 1
//   - All 4 hosts converge on both branch roots
//
// Why this matters: before the C-6 fix, branch-local mutations
// were rejected with "branch-local mutations on non-genesis branches
// not yet wired". Branches were decorative — they existed in the
// registry but couldn't be mutated. Now they work.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBranchMutation_AcceptsOnNonGenesis(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        94,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	// Step 1: BRANCH_CREATE → allocate branch 1.
	branchProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload: &pb.Transition_BranchCreate{BranchCreate: &pb.BranchCreatePayload{
			Reason: "experimental-events",
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canonB, err := group.MarshalCanonicalForSigningHelper(branchProto)
	if err != nil {
		t.Fatal(err)
	}
	sigsB := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonB)
		sigsB = append(sigsB, &pb.Signature{Raw: s[:]})
	}
	branchProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigsB}
	txB, err := group.NewTransition(branchProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, txB); err != nil {
			t.Fatalf("BRANCH_CREATE on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterBranch := w.Hosts()[0].State(gkp.Public).Root()
	if rootAfterBranch == parentRoot {
		t.Fatal("BRANCH_CREATE did not advance root")
	}
	t.Logf("BRANCH_CREATE applied; genesis root = %x", rootAfterBranch[:4])

	// Step 2: mutate branch 1 with a CREATE_EVENT targeting branch_id=1.
	// Branch 1 starts with an empty state (zero root), so prior_state
	// must be nil/empty (no prior state to match against).
	mutateProto := &pb.Transition{
		Type:     pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		BranchId: 1, // target the non-genesis branch
		Payload: &pb.Transition_CreateEvent{CreateEvent: &pb.CreateEventPayload{
			EventId: "branch-1-event",
			Title:   "Branch 1 Event",
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canonM, err := group.MarshalCanonicalForSigningHelper(mutateProto)
	if err != nil {
		t.Fatal(err)
	}
	sigsM := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonM)
		sigsM = append(sigsM, &pb.Signature{Raw: s[:]})
	}
	mutateProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigsM}
	txM, err := group.NewTransition(mutateProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	// Submit to all hosts.
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, txM); err != nil {
			t.Fatalf("branch-1 mutation on host %s should have succeeded: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	t.Logf("branch-1 mutation accepted on all hosts")

	// Genesis branch root must NOT have advanced.
	if got := w.Hosts()[0].State(gkp.Public).Root(); got != rootAfterBranch {
		t.Fatalf("genesis branch root advanced from branch-1 mutation: was %x, now %x", rootAfterBranch, got)
	}
	t.Logf("genesis branch root unchanged: %x", rootAfterBranch[:4])

	// Branch 1 should have a non-zero root (it has state now).
	b1 := w.Hosts()[0].State(gkp.Public).Branch(group.GenesisBranchID + 1)
	if b1 == nil {
		t.Fatal("branch 1 not found after mutation")
	}
	b1Root := b1.Root()
	var zero types.Hash
	if b1Root == zero {
		t.Fatal("branch 1 root is still zero after mutation — state not written")
	}
	t.Logf("branch 1 root after mutation: %x", b1Root[:4])

	// Verify all hosts converge on branch 1's root.
	for i, h := range w.Hosts()[1:] {
		b1h := h.State(gkp.Public).Branch(group.GenesisBranchID + 1)
		if b1h == nil {
			t.Fatalf("host %d: branch 1 not found", i+1)
		}
		if got := b1h.Root(); got != b1Root {
			t.Fatalf("host %d: branch 1 root %x != host 0 branch 1 root %x", i+1, got, b1Root)
		}
	}
	t.Logf("all 4 hosts converged on branch 1 root: %x", b1Root[:4])
}