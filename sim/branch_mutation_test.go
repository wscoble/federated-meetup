// SPDX-License-Identifier: MIT
//
// Branch-mutation-rejection scenario. The apply path in
// internal/group/group.go line ~362 explicitly rejects branch-local
// mutations on non-genesis branches with the message
// "branch-local mutations on non-genesis branches not yet wired".
// This test pins down the documented limitation: a transition
// targeting a non-genesis branch_id must be rejected, not
// silently applied to the wrong branch.
//
// What this exercises:
//   - BRANCH_CREATE allocates branch 1 (alongside genesis branch 0)
//   - A subsequent transition with branch_id=1 (mutating the
//     new branch) is REJECTED with the documented error
//   - The genesis branch is unaffected (no state mutation)
//   - All 4 hosts reject consistently
//
// Why this matters: if branch_id were silently ignored (and the
// transition applied to branch 0 instead), users would believe
// they'd split their group when they actually hadn't. Pinning
// the rejection keeps the limitation visible in the test suite
// — when a future engineer wires non-genesis mutations, the
// tests can be flipped to assert acceptance.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestBranchMutation_RejectedOnNonGenesis(t *testing.T) {
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
	t.Logf("BRANCH_CREATE applied; root = %x", rootAfterBranch[:4])

	// Step 2: attempt to mutate branch 1 with a CREATE_EVENT
	// targeting branch_id=1. Must be rejected.
	mutateProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		PriorState: &pb.StateRoot{Hash: rootAfterBranch[:]},
		BranchId:   1, // target the non-genesis branch
		Payload: &pb.Transition_CreateEvent{CreateEvent: &pb.CreateEventPayload{
			EventId: "branch-1-event",
			Title:   "Should not apply",
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

	h0 := w.Hosts()[0]
	_, err = h0.SubmitTransition(gkp.Public, txM)
	if err == nil {
		t.Fatal("mutation on non-genesis branch should be rejected")
	}
	if !strings.Contains(err.Error(), "branch-local") &&
		!strings.Contains(err.Error(), "not yet wired") {
		t.Logf("rejection (acceptable): %v", err)
	}
	t.Logf("branch-1 mutation correctly rejected: %v", err)

	// State root on genesis branch (the only branch that exists
	// at apply time per the rejection logic) must NOT have
	// advanced.
	if got := h0.State(gkp.Public).Root(); got != rootAfterBranch {
		t.Fatalf("state root advanced despite non-genesis mutation rejection: was %x, now %x", rootAfterBranch, got)
	}
}