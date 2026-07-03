// SPDX-License-Identifier: AGPL-3.0
//
// Cycle 44: transition log monotonicity under repeated identical payloads.
//
// ADD_STEWARD with the SAME key (cycle 33) advances the root via Seq++.
// This test verifies that the LOG grows monotonically even when many
// no-op transitions are submitted — the log captures every accepted
// transition regardless of whether it changes effective state.
//
// Why this matters: an audit consumer needs to see "alice tried to
// add bob 50 times" in the log even though the steward set only
// shows bob once. Seq++ makes the log total-ordered; without it,
// no-op transitions would be invisible.
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

func TestLog_MonotonicUnderRepeatedNoOps(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        107,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	newKP, _ := crypto.GenerateKey()

	// Submit ADD_STEWARD 10 times for the same key.
	const repeats = 10
	for i := 0; i < repeats; i++ {
		priorRoot := w.Hosts()[0].State(gkp.Public).Root()
		proto := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
			PriorState: &pb.StateRoot{Hash: priorRoot[:]},
			Payload: &pb.Transition_AddSteward{AddSteward: &pb.AddStewardPayload{
				NewSteward: &pb.PublicKey{Raw: newKP.Public[:]},
			}},
			SignedAt: timestamppb.New(w.Now().Add(time.Duration(i) * time.Millisecond)),
		}
		canon, _ := group.MarshalCanonicalForSigningHelper(proto)
		sigs := []*pb.Signature{}
		for _, k := range stewards[:2] {
			s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canon)
			sigs = append(sigs, &pb.Signature{Raw: s[:]})
		}
		proto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
		tx, _ := group.NewTransition(proto, gkp.Public)
		for _, h := range w.Hosts() {
			if _, err := h.SubmitTransition(gkp.Public, tx); err != nil {
				t.Fatalf("repeat %d on host %s: %v", i, h.ID(), err)
			}
		}
		w.Advance(2 * time.Millisecond)
		_ = parentRoot
	}

	// Verify the log has exactly `repeats` ADD_STEWARD entries for newKP
	// (plus the genesis CREATE_GROUP = 1, so log = repeats + 1).
	logLen := len(w.Hosts()[0].State(gkp.Public).Log())
	if logLen != repeats+1 {
		t.Errorf("log length = %d, want %d", logLen, repeats+1)
	}

	// Verify steward set has newKP exactly once.
	stewardsNow := w.Hosts()[0].State(gkp.Public).Stewards()
	var count int
	for _, s := range stewardsNow {
		if s.Key == newKP.Public {
			count++
		}
	}
	if count != 1 {
		t.Errorf("steward newKP appears %d times, want 1", count)
	}

	t.Logf("log captured %d ADD_STEWARD attempts; steward set has newKP exactly once", repeats)
}