// SPDX-License-Identifier: AGPL-3.0
//
// Threshold-1 minimum-trust boundary. A 1-of-N group is the
// minimum-viable trust configuration: any single active steward
// can authorize a transition. This exercises the boundary
// between "single point of failure" and "operational simplicity."
//
// What this exercises:
//   - A 1-of-1 group (single steward, threshold 1) accepts
//     transitions signed by that steward alone
//   - ADD_STEWARD grows the set to 2 stewards; threshold stays 1
//     (ADD_STEWARD doesn't auto-raise the threshold)
//   - After ADD_STEWARD, EITHER steward alone can authorize
//     a transition (threshold still 1)
//   - REMOVE_STEWARD can shrink the set back to 1
//   - All 4 hosts converge at every step
//
// Why this matters: the threshold-1 case is a real product
// configuration — solo founders running their own groups, single
// trusted-organizer book clubs, etc. The protocol must support
// it without surprise restrictions.
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

func TestThreshold1_SoloSteward(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        89,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	// Derive a single-steward keypair.
	var aliceSeed [32]byte
	as := w.DeriveSeed("solo-alice")
	for j := 0; j < 8; j++ {
		aliceSeed[j] = byte(as >> (8 * j))
	}
	alice := crypto.KeyPairFromSeed(aliceSeed)

	// Derive the group key.
	var groupSeed [32]byte
	gid := w.DeriveSeed("solo-group")
	for j := 0; j < 8; j++ {
		groupSeed[j] = byte(gid >> (8 * j))
	}
	groupKP := crypto.KeyPairFromSeed(groupSeed)

	// CREATE_GROUP with [alice] as the only steward, threshold 1.
	createPayload := &pb.CreateGroupPayload{
		CanonicalName:   "solo-test",
		DisplayName:     "Solo Test Group",
		InitialStewards: stewardPBs([]crypto.KeyPair{alice}),
		Threshold:       1,
	}
	createTr := &pb.Transition{
		Type:    pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload: &pb.Transition_CreateGroup{CreateGroup: createPayload},
		SignedAt: timestamppb.New(w.Now()),
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(createTr)
	if err != nil {
		t.Fatal(err)
	}
	sig := crypto.Sign(alice, groupKP.Public, crypto.MsgKindTransition, canonical)
	createTr.StewardSignatures = &pb.Multisig{
		Threshold:  1,
		Signatures: []*pb.Signature{{Raw: sig[:]}},
	}
	tx, err := group.NewTransition(createTr, groupKP.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		h.AddGroup(groupKP.Public, tx)
		if _, err := h.SubmitTransition(groupKP.Public, tx); err != nil {
			t.Fatalf("CREATE_GROUP on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	parentRoot := w.Hosts()[0].State(groupKP.Public).Root()
	if peers := w.Hosts()[0].State(groupKP.Public).StewardsAt(nil); len(peers) != 1 {
		t.Fatalf("expected 1 steward post-CREATE, got %d", len(peers))
	}
	t.Logf("CREATE applied; 1 steward, threshold 1; root = %x", parentRoot[:4])

	// Add bob as second steward. Threshold stays 1.
	var bobSeed [32]byte
	bs := w.DeriveSeed("solo-bob")
	for j := 0; j < 8; j++ {
		bobSeed[j] = byte(bs >> (8 * j))
	}
	bob := crypto.KeyPairFromSeed(bobSeed)

	addPayload := &pb.AddStewardPayload{
		NewSteward: &pb.PublicKey{Raw: bob.Public[:]},
	}
	addTr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload:    &pb.Transition_AddSteward{AddSteward: addPayload},
		SignedAt:   timestamppb.New(w.Now()),
	}
	canonicalAdd, err := group.MarshalCanonicalForSigningHelper(addTr)
	if err != nil {
		t.Fatal(err)
	}
	sigAdd := crypto.Sign(alice, groupKP.Public, crypto.MsgKindTransition, canonicalAdd)
	addTr.StewardSignatures = &pb.Multisig{
		Threshold:  1,
		Signatures: []*pb.Signature{{Raw: sigAdd[:]}},
	}
	addTx, err := group.NewTransition(addTr, groupKP.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(groupKP.Public, addTx); err != nil {
			t.Fatalf("ADD_STEWARD on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterAdd := w.Hosts()[0].State(groupKP.Public).Root()
	if peers := w.Hosts()[0].State(groupKP.Public).StewardsAt(nil); len(peers) != 2 {
		t.Fatalf("expected 2 stewards post-ADD, got %d", len(peers))
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(groupKP.Public).Root(); got != rootAfterAdd {
			t.Fatalf("post-ADD divergence: host %s=%x want %x", h.ID(), got, rootAfterAdd)
		}
	}
	t.Logf("ADD_STEWARD applied; 2 stewards, threshold 1; root = %x", rootAfterAdd[:4])

	// Now sign a transition with BOB alone — must succeed because
	// threshold is 1.
	evtPayload := &pb.CreateEventPayload{
		EventId: "thresh1-event",
		Title:   "Bob-signed event",
	}
	evtTr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		PriorState: &pb.StateRoot{Hash: rootAfterAdd[:]},
		Payload:    &pb.Transition_CreateEvent{CreateEvent: evtPayload},
		SignedAt:   timestamppb.New(w.Now()),
	}
	canonicalEvt, err := group.MarshalCanonicalForSigningHelper(evtTr)
	if err != nil {
		t.Fatal(err)
	}
	sigEvt := crypto.Sign(bob, groupKP.Public, crypto.MsgKindTransition, canonicalEvt)
	evtTr.StewardSignatures = &pb.Multisig{
		Threshold:  1,
		Signatures: []*pb.Signature{{Raw: sigEvt[:]}},
	}
	evtTx, err := group.NewTransition(evtTr, groupKP.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(groupKP.Public, evtTx); err != nil {
			t.Fatalf("bob-only CREATE_EVENT on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterEvt := w.Hosts()[0].State(groupKP.Public).Root()
	if rootAfterEvt == rootAfterAdd {
		t.Fatal("bob-only CREATE_EVENT did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(groupKP.Public).Root(); got != rootAfterEvt {
			t.Fatalf("post-EVT divergence: host %s=%x want %x", h.ID(), got, rootAfterEvt)
		}
	}
	t.Logf("bob-only CREATE_EVENT applied at threshold 1; root = %x", rootAfterEvt[:4])

	// REMOVE_STEWARD alice (bob signs alone). Threshold still 1.
	rmPayload := &pb.RemoveStewardPayload{
		Steward: &pb.PublicKey{Raw: alice.Public[:]},
	}
	rmTr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_REMOVE_STEWARD,
		PriorState: &pb.StateRoot{Hash: rootAfterEvt[:]},
		Payload:    &pb.Transition_RemoveSteward{RemoveSteward: rmPayload},
		SignedAt:   timestamppb.New(w.Now()),
	}
	canonicalRm, err := group.MarshalCanonicalForSigningHelper(rmTr)
	if err != nil {
		t.Fatal(err)
	}
	sigRm := crypto.Sign(bob, groupKP.Public, crypto.MsgKindTransition, canonicalRm)
	rmTr.StewardSignatures = &pb.Multisig{
		Threshold:  1,
		Signatures: []*pb.Signature{{Raw: sigRm[:]}},
	}
	rmTx, err := group.NewTransition(rmTr, groupKP.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(groupKP.Public, rmTx); err != nil {
			t.Fatalf("REMOVE_STEWARD on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	if peers := w.Hosts()[0].State(groupKP.Public).StewardsAt(nil); len(peers) != 1 {
		t.Fatalf("expected 1 steward post-REMOVE, got %d", len(peers))
	}
	t.Logf("REMOVE_STEWARD alice succeeded; back to 1 steward (bob), threshold 1")
}