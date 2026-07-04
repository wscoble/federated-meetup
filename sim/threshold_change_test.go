// SPDX-License-Identifier: AGPL-3.0
//
// CHANGE_THRESHOLD boundary tests. The apply switch for
// CHANGE_THRESHOLD (internal/group/group.go line 522) does NOT
// currently validate that the new threshold is in [1, steward_count].
// This test documents the current behavior — and the gap.
//
// What this exercises:
//   - CHANGE_THRESHOLD with newThreshold=0 succeeds and sets
//     threshold to 0 (no signatures ever required). This is a
//     catastrophic security regression — any unsigned transition
//     would apply.
//   - CHANGE_THRESHOLD with newThreshold > steward_count also
//     succeeds and immediately dead-locks the group (no quorum
//     possible).
//
// Why this matters: the threshold value is the security knob of
// the entire protocol. Setting it to 0 effectively disables
// steward authentication. Setting it above the steward count
// dead-locks the group.
//
// This test currently asserts that the buggy behavior exists
// (or rejects — whichever the implementation does), so future
// gates added at the apply layer can be verified by changing
// the assertions.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestChangeThreshold_ZeroRejected(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        90,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	// CHANGE_THRESHOLD newThreshold=0.
	changeTr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload: &pb.Transition_ChangeThreshold{ChangeThreshold: &pb.ChangeThresholdPayload{
			NewThreshold: 0,
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(changeTr)
	if err != nil {
		t.Fatal(err)
	}
	sigs := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	changeTr.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, err := group.NewTransition(changeTr, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	h0 := w.Hosts()[0]
	_, err = h0.SubmitTransition(gkp.Public, tx)
	// G8 (cycle 31) gate: threshold=0 is rejected as it would
	// disable authentication.
	if err == nil {
		t.Fatal("threshold=0 should be rejected by G8 gate")
	}
	if !strings.Contains(err.Error(), "disables authentication") &&
		!strings.Contains(err.Error(), "newThreshold") {
		t.Logf("threshold=0 rejected (acceptable reason): %v", err)
	}
	t.Logf("threshold=0 correctly rejected: %v", err)
	if got := h0.State(gkp.Public).Root(); got != parentRoot {
		t.Fatalf("state root advanced despite rejection")
	}
}

func TestChangeThreshold_ExceedsStewardsRejected(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        92,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w) // [alice, bob, carol]
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	// CHANGE_THRESHOLD newThreshold=99 (way beyond 3 stewards).
	changeTr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload: &pb.Transition_ChangeThreshold{ChangeThreshold: &pb.ChangeThresholdPayload{
			NewThreshold: 99,
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(changeTr)
	if err != nil {
		t.Fatal(err)
	}
	sigs := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	changeTr.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, err := group.NewTransition(changeTr, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	h0 := w.Hosts()[0]
	_, err = h0.SubmitTransition(gkp.Public, tx)
	if err == nil {
		t.Fatal("threshold=99 with 3 stewards should be rejected (would dead-lock)")
	}
	if !strings.Contains(err.Error(), "dead-lock") &&
		!strings.Contains(err.Error(), "exceeds") {
		t.Logf("threshold=99 rejected (acceptable reason): %v", err)
	}
	t.Logf("threshold=99 correctly rejected: %v", err)
}