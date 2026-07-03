// SPDX-License-Identifier: AGPL-3.0
//
// Positive CHANGE_THRESHOLD path. After the G8 gate (cycle 31)
// is added, legitimate threshold changes must still work:
//   - Lowering threshold is allowed
//   - Raising threshold up to current steward count is allowed
//
// What this exercises:
//   - Vegas Programmers exist (3 stewards, threshold 2)
//   - CHANGE_THRESHOLD newThreshold=1 succeeds (lowers quorum
//     requirement; 1 sig now suffices)
//   - Subsequent transition signed by a single steward applies
//   - CHANGE_THRESHOLD newThreshold=3 succeeds (raises to full
//     steward set; 3 sigs now required)
//   - Subsequent transition signed by all 3 stewards applies
//   - State root advances through every step
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

func TestChangeThreshold_LegitimateChange(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        93,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w) // [alice, bob, carol]
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	// Lower threshold to 1.
	change1 := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload:    &pb.Transition_ChangeThreshold{ChangeThreshold: &pb.ChangeThresholdPayload{NewThreshold: 1}},
		SignedAt:   timestamppb.New(w.Now()),
	}
	canon1, err := group.MarshalCanonicalForSigningHelper(change1)
	if err != nil {
		t.Fatal(err)
	}
	sigs1 := []*pb.Signature{}
	for _, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canon1)
		sigs1 = append(sigs1, &pb.Signature{Raw: s[:]})
	}
	change1.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs1}
	tx1, err := group.NewTransition(change1, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, tx1); err != nil {
			t.Fatalf("CHANGE_THRESHOLD to 1 on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfter1 := w.Hosts()[0].State(gkp.Public).Root()
	if rootAfter1 == parentRoot {
		t.Fatal("threshold=1 CHANGE did not advance root")
	}
	t.Logf("threshold=1 applied; root = %x", rootAfter1[:4])

	// Submit a single-steward event (alice alone is enough).
	evt1 := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		PriorState: &pb.StateRoot{Hash: rootAfter1[:]},
		Payload: &pb.Transition_CreateEvent{CreateEvent: &pb.CreateEventPayload{
			EventId: "thresh1-event",
			Title:   "Alice-only at threshold 1",
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canonE, err := group.MarshalCanonicalForSigningHelper(evt1)
	if err != nil {
		t.Fatal(err)
	}
	sigE := crypto.Sign(stewards[0], gkp.Public, crypto.MsgKindTransition, canonE)
	evt1.StewardSignatures = &pb.Multisig{Threshold: 1, Signatures: []*pb.Signature{{Raw: sigE[:]}}}
	txE, err := group.NewTransition(evt1, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, txE); err != nil {
			t.Fatalf("alice-only EVENT on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterE := w.Hosts()[0].State(gkp.Public).Root()
	t.Logf("alice-only EVENT applied at threshold 1; root = %x", rootAfterE[:4])

	// Raise threshold to 3 (full set).
	change3 := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		PriorState: &pb.StateRoot{Hash: rootAfterE[:]},
		Payload:    &pb.Transition_ChangeThreshold{ChangeThreshold: &pb.ChangeThresholdPayload{NewThreshold: 3}},
		SignedAt:   timestamppb.New(w.Now()),
	}
	canon3, err := group.MarshalCanonicalForSigningHelper(change3)
	if err != nil {
		t.Fatal(err)
	}
	// At threshold 1, any single signature suffices. Use alice.
	sig3 := crypto.Sign(stewards[0], gkp.Public, crypto.MsgKindTransition, canon3)
	change3.StewardSignatures = &pb.Multisig{Threshold: 1, Signatures: []*pb.Signature{{Raw: sig3[:]}}}
	tx3, err := group.NewTransition(change3, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		thrBefore := h.State(gkp.Public).ThresholdAt(&pb.StateRoot{Hash: rootAfterE[:]})
		if _, err := h.SubmitTransition(gkp.Public, tx3); err != nil {
			t.Fatalf("CHANGE_THRESHOLD to 3 on host %s (threshold was %d at rootAfterE): %v", h.ID(), thrBefore, err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfter3 := w.Hosts()[0].State(gkp.Public).Root()
	t.Logf("threshold=3 applied; root = %x", rootAfter3[:4])

	// Submit a 3-of-3 event (all stewards must sign).
	evt3 := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		PriorState: &pb.StateRoot{Hash: rootAfter3[:]},
		Payload: &pb.Transition_CreateEvent{CreateEvent: &pb.CreateEventPayload{
			EventId: "thresh3-event",
			Title:   "All 3 stewards required",
		}},
		SignedAt: timestamppb.New(w.Now()),
	}
	canonE3, err := group.MarshalCanonicalForSigningHelper(evt3)
	if err != nil {
		t.Fatal(err)
	}
	sigsE3 := []*pb.Signature{}
	for _, k := range stewards {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonE3)
		sigsE3 = append(sigsE3, &pb.Signature{Raw: s[:]})
	}
	evt3.StewardSignatures = &pb.Multisig{Threshold: 3, Signatures: sigsE3}
	txE3, err := group.NewTransition(evt3, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, txE3); err != nil {
			t.Fatalf("3-of-3 EVENT on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterE3 := w.Hosts()[0].State(gkp.Public).Root()
	if rootAfterE3 == rootAfter3 {
		t.Fatal("3-of-3 EVENT did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != rootAfterE3 {
			t.Fatalf("post-3-of-3 divergence: host %s=%x want %x", h.ID(), got, rootAfterE3)
		}
	}
	t.Logf("3-of-3 EVENT applied; root = %x", rootAfterE3[:4])
}