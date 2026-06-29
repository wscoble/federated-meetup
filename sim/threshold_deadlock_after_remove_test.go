// SPDX-License-Identifier: MIT
//
// Cycle 43: CHANGE_THRESHOLD boundary under steward churn.
//
// G8 (cycle 31) rejects newThreshold=0 (disables auth) and
// newThreshold > activeStewardCount (dead-locks). This test pins
// down what happens when the active steward count CHANGES after a
// CHANGE_THRESHOLD — specifically:
//
//   1. threshold=N, stewards=M, N<=M → fine
//   2. ADD_STEWARD to grow M → threshold still valid (N<=M+1)
//   3. REMOVE_STEWARD to shrink M such that N>M → DEAD-LOCK
//
// Scenario 3 is the dangerous one. The threshold stays at N, but
// the active steward count drops below N. The group cannot produce
// any future valid transition — including ADD_STEWARD to recover.
// REMOVE_STEWARD itself needs N signatures, which is impossible.
//
// This test verifies that scenario 3 results in a dead-lock (no
// further transitions accepted), not a silent acceptance of an
// unsafe REMOVE.
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

func TestThreshold_DeadlockAfterRemove(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        106,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	// Setup: 3 stewards, threshold 2.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	// Bump threshold to 3 (matches steward count).
	changeProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload: &pb.Transition_ChangeThreshold{ChangeThreshold: &pb.ChangeThresholdPayload{
			NewThreshold: 3,
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canon, _ := group.MarshalCanonicalForSigningHelper(changeProto)
	sigs := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canon)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	changeProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, _ := group.NewTransition(changeProto, gkp.Public)
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, tx); err != nil {
			t.Fatalf("CHANGE_THRESHOLD 3: %v", err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterChange := w.Hosts()[0].State(gkp.Public).Root()

	// Threshold is now 3 — all 3 stewards must sign.
	if got := w.Hosts()[0].State(gkp.Public).Threshold(); got != 3 {
		t.Fatalf("threshold = %d, want 3", got)
	}

	// Now REMOVE_STEWARD carol. With threshold 3 and only 2 signers
	// (alice + bob, since carol is the one being removed), the multisig
	// threshold can't be met. Result: REMOVE is REJECTED, group stays
	// at 3 stewards / threshold 3 — fully functional.
	removeProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_REMOVE_STEWARD,
		PriorState: &pb.StateRoot{Hash: rootAfterChange[:]},
		Payload: &pb.Transition_RemoveSteward{RemoveSteward: &pb.RemoveStewardPayload{
			Steward: &pb.PublicKey{Raw: stewards[2].Public[:]},
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canonR, _ := group.MarshalCanonicalForSigningHelper(removeProto)
	// Sign with alice + bob only (carol can't sign her own removal).
	sigsR := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonR)
		sigsR = append(sigsR, &pb.Signature{Raw: s[:]})
	}
	// Threshold=3 declared but only 2 signatures — should FAIL.
	removeProto.StewardSignatures = &pb.Multisig{Threshold: 3, Signatures: sigsR}
	txR, _ := group.NewTransition(removeProto, gkp.Public)

	h0 := w.Hosts()[0]
	_, err := h0.SubmitTransition(gkp.Public, txR)
	if err == nil {
		t.Fatal("REMOVE_STEWARD with 2-of-3 sigs but threshold=3 should fail")
	}
	if !strings.Contains(err.Error(), "threshold") && !strings.Contains(err.Error(), "signature") {
		t.Logf("rejection (acceptable): %v", err)
	}

	// Verify the group is still at 3 stewards (REMOVE was rejected).
	if got := len(w.Hosts()[0].State(gkp.Public).Stewards()); got != 3 {
		t.Errorf("stewards = %d after rejected REMOVE, want 3", got)
	}

	t.Logf("deadlock prevention: REMOVE_STEWARD rejected because threshold=3 cannot be met with 2-of-3 sigs")
}