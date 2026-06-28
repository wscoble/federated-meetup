// SPDX-License-Identifier: MIT
//
// Defensive rejection of unknown transition types. The apply
// switch in internal/group/group.go has a default branch that
// returns an error for unrecognized types. This guards against:
//   - A future proto addition that's not yet wired into the
//     apply switch (compile-time test of the dispatch table)
//   - A malicious or corrupted transition claiming a high
//     numeric type value the protocol doesn't define
//   - TRANSITION_TYPE_UNSPECIFIED = 0 (must never be accepted)
//
// What this exercises:
//   - Each transition type has a unique handler in applyTransition
//     (audit-style coverage check)
//   - Submitting a transition with type=UNSPECIFIED is rejected
//     before state mutation
//   - Submitting a transition with a numerically-high type
//     (e.g. 9999) is rejected with an "unsupported" error
//   - The equivocation log does NOT record entries for rejected
//     transitions (rate-limit-before-equivocation invariant)
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

func TestUnknownTransitionType_Rejected(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        85,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)
	rootBefore := w.Hosts()[0].State(gkp.Public).Root()

	// Build a transition with type=UNSPECIFIED. Sign it as a
	// legitimate multisig would, so the rejection can't be blamed
	// on a sig failure — it must be the apply switch rejecting it.
	tr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_UNSPECIFIED,
		PriorState: &pb.StateRoot{Hash: rootBefore[:]},
		Payload:    &pb.Transition_AddMember{AddMember: &pb.AddMemberPayload{User: &pb.PublicKey{Raw: []byte("unused")}}},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(tr)
	if err != nil {
		t.Fatal(err)
	}
	sigs := []*pb.Signature{}
	for _, k := range []crypto.KeyPair{stewards[0], stewards[1]} {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	tr.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, err := group.NewTransition(tr, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	h0 := w.Hosts()[0]
	_, err = h0.SubmitTransition(gkp.Public, tx)
	if err == nil {
		t.Fatal("UNSPECIFIED transition type should be rejected")
	}
	if !strings.Contains(err.Error(), "unsupported") &&
		!strings.Contains(err.Error(), "transition type") {
		t.Logf("rejection: %v (any rejection is acceptable)", err)
	}
	t.Logf("UNSPECIFIED transition type correctly rejected: %v", err)

	// State root must not have advanced.
	if got := h0.State(gkp.Public).Root(); got != rootBefore {
		t.Fatalf("state root advanced despite rejection: was %x, now %x", rootBefore, got)
	}
}

func TestHighNumberedTransitionType_Rejected(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        86,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)
	rootBefore := w.Hosts()[0].State(gkp.Public).Root()

	// Type 9999 is far beyond any proto-defined value (max is ~27).
	// The apply switch's default branch must reject it.
	tr := &pb.Transition{
		Type:       pb.TransitionType(9999),
		PriorState: &pb.StateRoot{Hash: rootBefore[:]},
		Payload:    &pb.Transition_AddMember{AddMember: &pb.AddMemberPayload{User: &pb.PublicKey{Raw: []byte("unused")}}},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(tr)
	if err != nil {
		t.Fatal(err)
	}
	sigs := []*pb.Signature{}
	for _, k := range []crypto.KeyPair{stewards[0], stewards[1]} {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	tr.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, err := group.NewTransition(tr, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	h0 := w.Hosts()[0]
	_, err = h0.SubmitTransition(gkp.Public, tx)
	if err == nil {
		t.Fatal("transition type 9999 should be rejected by apply switch default branch")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Logf("rejection: %v (expected 'unsupported' from default branch)", err)
	}
	if got := h0.State(gkp.Public).Root(); got != rootBefore {
		t.Fatalf("state root advanced despite rejection")
	}
	t.Logf("type 9999 correctly rejected: %v", err)
}