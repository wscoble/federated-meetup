// SPDX-License-Identifier: AGPL-3.0
//
// No-op transition characterization. ADD_STEWARD against a key that's
// already in the steward set does NOT change the *value* the KV stores
// for that key (appendOrUpdate rewrites []byte{1} over []byte{1}).
// But — and this is the deep design choice — every accepted transition
// increments the per-key Seq counter, which is part of the Merkle leaf.
// So a duplicate ADD_STEWARD DOES advance the state root, even though
// the steward set itself is unchanged.
//
// What this guarantees:
//
//  1. The state root is monotonic: every accepted transition produces
//     a new root. There is no way for two hosts with the same prior
//     root to disagree about whether a transition was accepted — they
//     either both computed the same new root, or both rejected it.
//
//  2. The transition log is total-ordered by root: no two transitions
//     share the same root, so log entries are unambiguously orderable.
//
//  3. The steward set is correctly deduped: re-adding an existing
//     steward does not double-count them (prospectiveStewardsAfterAddLocked
//     returns the existing set unchanged on duplicate, see group.go:879).
//
// This test pins all three properties down at once. If any of them
// changes (e.g., seq removed from the Merkle leaf, dedupe logic
// regressed, log accepting duplicate roots), this test fails.
package sim_test

import (
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNoOp_AddStewardAccepted_NoRootAdvance(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        95,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	h0 := w.Hosts()[0]
	priorRoot := h0.State(gkp.Public).Root()

	// Build ADD_STEWARD for "steward-X" — a brand-new key.
	newKP, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	addProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		PriorState: &pb.StateRoot{Hash: priorRoot[:]},
		Payload: &pb.Transition_AddSteward{AddSteward: &pb.AddStewardPayload{
			NewSteward: &pb.PublicKey{Raw: newKP.Public[:]},
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canon, err := group.MarshalCanonicalForSigningHelper(addProto)
	if err != nil {
		t.Fatal(err)
	}
	sigs := sigsFor(stewards[:2], gkp.Public, canon)
	addProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	txAdd, err := group.NewTransition(addProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	// First submission: should advance the root.
	if _, err := h0.SubmitTransition(gkp.Public, txAdd); err != nil {
		t.Fatalf("first ADD_STEWARD: %v", err)
	}
	rootAfterFirst := h0.State(gkp.Public).Root()
	if rootAfterFirst == priorRoot {
		t.Fatal("first ADD_STEWARD did not advance root")
	}
	logAfterFirst := len(h0.State(gkp.Public).Log())
	stewardsAfterFirst := h0.State(gkp.Public).Stewards()

	// Build a SECOND ADD_STEWARD for the SAME key, but with a fresh HLC.
	addProto2 := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		PriorState: &pb.StateRoot{Hash: rootAfterFirst[:]},
		Payload: &pb.Transition_AddSteward{AddSteward: &pb.AddStewardPayload{
			NewSteward: &pb.PublicKey{Raw: newKP.Public[:]},
		}},
		SignedAt: timestamppb.New(w.Now().Add(50 * time.Millisecond)),
	}
	canon2, _ := group.MarshalCanonicalForSigningHelper(addProto2)
	sigs2 := sigsFor(stewards[:2], gkp.Public, canon2)
	addProto2.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs2}
	txAdd2, err := group.NewTransition(addProto2, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	// Second submission: still applies (not rejected). Root DOES
	// advance because appendOrUpdate increments the per-key Seq
	// counter (the Merkle leaf includes Seq, not just Key+Value).
	if _, err := h0.SubmitTransition(gkp.Public, txAdd2); err != nil {
		t.Fatalf("second ADD_STEWARD (duplicate) was REJECTED — behavior changed: %v", err)
	}
	rootAfterSecond := h0.State(gkp.Public).Root()
	if rootAfterSecond == rootAfterFirst {
		t.Fatal("duplicate ADD_STEWARD did not advance root — Seq counter may not be part of Merkle leaf")
	}

	// Log grew by exactly 1 (the duplicate was appended).
	if got := len(h0.State(gkp.Public).Log()); got != logAfterFirst+1 {
		t.Fatalf("expected log to grow by 1, got %d -> %d", logAfterFirst, got)
	}

	// Verify the steward set is unchanged (deduped in computeCurrentStewards).
	stewardsAfterSecond := h0.State(gkp.Public).Stewards()
	if len(stewardsAfterSecond) != len(stewardsAfterFirst) {
		t.Fatalf("steward set grew on duplicate ADD_STEWARD: was %d, now %d",
			len(stewardsAfterFirst), len(stewardsAfterSecond))
	}
	countX := 0
	for _, s := range stewardsAfterSecond {
		if s.Key == newKP.Public {
			countX++
		}
	}
	if countX != 1 {
		t.Fatalf("steward-X appears %d times in steward set; want 1", countX)
	}
	t.Logf("duplicate ADD_STEWARD accepted, root advanced (Seq++), log grew, steward set deduped")
}