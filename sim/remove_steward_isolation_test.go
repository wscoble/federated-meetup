// SPDX-License-Identifier: AGPL-3.0
//
// REMOVE_STEWARD isolation test: apply REMOVE_STEWARD on a fresh 3-steward
// group (threshold 2), check whether the state advances. Cuts down to the
// minimum scenario so we can see exactly what happens.
package sim_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	"github.com/wscoble/federated-meetup/internal/hlc"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
)

// TestIsolation_RemoveSteward is the minimum repro for the
// "REMOVE_STEWARD doesn't advance state" finding from
// TestLifecycle_FullStateMachine. Uses a 3-steward, threshold-2 group
// (no CHANGE_THRESHOLD involved) so we isolate REMOVE_STEWARD's behavior.
func TestIsolation_RemoveSteward(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        100,
		HostCount:   1, // single host, no convergence noise
		InitialTime: time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	// Build a 3-steward, threshold-2 group directly.
	stewardSeeds := []uint64{
		w.DeriveSeed("alice"),
		w.DeriveSeed("bob"),
		w.DeriveSeed("carol"),
	}
	stewards := make([]crypto.KeyPair, 3)
	for i, s := range stewardSeeds {
		var seed [32]byte
		for j := 0; j < 8; j++ {
			seed[j] = byte(s >> (8 * j))
		}
		stewards[i] = crypto.KeyPairFromSeed(seed)
	}

	var groupSeed [32]byte
	gid := w.DeriveSeed("group")
	for j := 0; j < 8; j++ {
		groupSeed[j] = byte(gid >> (8 * j))
	}
	gkp := crypto.KeyPairFromSeed(groupSeed)

	// CREATE_GROUP: alice + bob (threshold 2)
	cgPayload := &pb.CreateGroupPayload{
		CanonicalName:   "test-isolation",
		DisplayName:     "Test Isolation",
		InitialStewards: stewardPBs(stewards),
		Threshold:       2,
	}
	cgProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		PriorState: nil,
		Payload:    &pb.Transition_CreateGroup{CreateGroup: cgPayload},
		SignedAt:   timestamppb.New(w.Now()),
	}
	cgCanonical, _ := group.MarshalCanonicalForSigningHelper(cgProto)
	cgProto.StewardSignatures = &pb.Multisig{
		Threshold:  2,
		Signatures: sigsFor(stewards[:2], gkp.Public, cgCanonical),
	}
	cgTr, err := group.NewTransition(cgProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	h := w.Hosts()[0]
	h.AddGroup(gkp.Public, cgTr)
	if _, err := h.SubmitTransition(gkp.Public, cgTr); err != nil {
		t.Fatal(err)
	}

	rootAfterCG := h.State(gkp.Public).Root()
	stewardsAtCG := h.State(gkp.Public).Stewards()
	entriesAtCG := h.State(gkp.Public).Snapshot().Entries
	t.Logf("after CREATE_GROUP: root=%x stewards=%d entries=%d", rootAfterCG[:4], len(stewardsAtCG), len(entriesAtCG))
	for i, s := range stewardsAtCG {
		t.Logf("  steward[%d] = %x", i, s.Key[:4])
	}
	for _, e := range entriesAtCG {
		t.Logf("  entry: %s = %x (seq=%d)", e.Key, e.Value, e.Seq)
	}

	// Now REMOVE_STEWARD carol. Sign with alice + bob (threshold 2).
	rmPayload := &pb.RemoveStewardPayload{Steward: &pb.PublicKey{Raw: stewards[2].Public[:]}}
	rmProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_REMOVE_STEWARD,
		PriorState: &pb.StateRoot{Hash: rootAfterCG[:]},
		Payload:    &pb.Transition_RemoveSteward{RemoveSteward: rmPayload},
		SignedAt:   timestamppb.New(w.Now().Add(5 * time.Millisecond)),
	}
	rmCanonical, _ := group.MarshalCanonicalForSigningHelper(rmProto)
	rmProto.StewardSignatures = &pb.Multisig{
		Threshold:  2,
		Signatures: sigsFor([]crypto.KeyPair{stewards[0], stewards[1]}, gkp.Public, rmCanonical),
	}
	rmTr, err := group.NewTransition(rmProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	rmTr.Proto.Hlc = hlc.New(w.Now().Add(10 * time.Millisecond))

	if _, err := h.SubmitTransition(gkp.Public, rmTr); err != nil {
		t.Fatalf("REMOVE_STEWARD failed: %v", err)
	}

	rootAfterRM := h.State(gkp.Public).Root()
	stewardsAfterRM := h.State(gkp.Public).Stewards()
	entriesAfterRM := h.State(gkp.Public).Snapshot().Entries
	t.Logf("after REMOVE_STEWARD carol: root=%x stewards=%d entries=%d", rootAfterRM[:4], len(stewardsAfterRM), len(entriesAfterRM))
	for i, s := range stewardsAfterRM {
		t.Logf("  steward[%d] = %x", i, s.Key[:4])
	}
	for _, e := range entriesAfterRM {
		t.Logf("  entry: %s = %x (seq=%d)", e.Key, e.Value, e.Seq)
	}

	if rootAfterRM == rootAfterCG {
		t.Errorf("REMOVE_STEWARD did not advance the state root")
	}
	if len(stewardsAfterRM) != 2 {
		t.Errorf("expected 2 stewards after REMOVE_STEWARD, got %d", len(stewardsAfterRM))
	}
}