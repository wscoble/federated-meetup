// SPDX-License-Identifier: AGPL-3.0
//
// Lifecycle scenario: drive a group through the full happy-path state machine
// across 4 hosts in the sim. Exercises CREATE_GROUP + ADD_STEWARD +
// REMOVE_STEWARD + CHANGE_THRESHOLD + ADD_MEMBER + REMOVE_MEMBER +
// CREATE_EVENT + UPDATE_EVENT + CANCEL_EVENT + RSVP. Asserts convergence.
//
// This is the experiment. The point is NOT to prove every transition works in
// isolation (existing unit tests do that). The point is to prove that
// applying 9+ transitions in sequence on a 4-host virtual federation
// converges to the same state root, and to surface what breaks.
//
// Same seed → same result. Always.
package sim_test

import (
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/hlc"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// TestLifecycle_FullStateMachine walks 9 transition types across 4 hosts.
// Fails on first divergence. Seed 7 → reproducible.
func TestLifecycle_FullStateMachine(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        7,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	// setupVegasProgrammers already applies CREATE_GROUP on all 4 hosts.
	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w) // [alice, bob, carol]

	// ─── 1. ADD_STEWARD — bring in dave (4th steward) ────────────────────
	daveKP := keyPairFromSeed(w, "dave")
	davePB := &pb.PublicKey{Raw: daveKP.Public[:]}

	if !applyBroadcast(t, w, gkp, "ADD_STEWARD dave",
		pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		&pb.AddStewardPayload{NewSteward: davePB},
		[]crypto.KeyPair{stewards[0], stewards[1]}, // alice + bob (threshold 2)
	) {
		return
	}

	// ─── 2. CHANGE_THRESHOLD — bump to 3 ────────────────────────────────
	if !applyBroadcast(t, w, gkp, "CHANGE_THRESHOLD 3",
		pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		&pb.ChangeThresholdPayload{NewThreshold: 3},
		[]crypto.KeyPair{stewards[0], stewards[1], stewards[2]}, // alice + bob + carol
	) {
		return
	}

	// ─── 3. ADD_MEMBER — add eve ────────────────────────────────────────
	eveKP := keyPairFromSeed(w, "eve")
	evePB := &pb.PublicKey{Raw: eveKP.Public[:]}

	// Threshold is now 3 (set in step 2). ADD_MEMBER is gated by the
	// steward-set threshold per §4 of the protocol — so we need 3 sigs.
	if !applyBroadcast(t, w, gkp, "ADD_MEMBER eve",
		pb.TransitionType_TRANSITION_TYPE_ADD_MEMBER,
		&pb.AddMemberPayload{User: evePB},
		[]crypto.KeyPair{stewards[0], stewards[1], stewards[2]}, // alice + bob + carol
	) {
		return
	}

	// ─── 4. CREATE_EVENT ────────────────────────────────────────────────
	startsAt := timestamppb.New(time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC))
	if !applyBroadcast(t, w, gkp, "CREATE_EVENT",
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		&pb.CreateEventPayload{
			EventId:  "evt-001",
			Title:    "First Vegas Programmers meetup",
			StartsAt: startsAt,
			Location: "Goldwell Open Air Museum",
			Capacity: 50,
		},
		[]crypto.KeyPair{stewards[0], stewards[1], daveKP}, // alice + bob + dave (threshold 3)
	) {
		return
	}

	// ─── 5. UPDATE_EVENT — patch the capacity ───────────────────────────
	if !applyBroadcast(t, w, gkp, "UPDATE_EVENT patch capacity=75",
		pb.TransitionType_TRANSITION_TYPE_UPDATE_EVENT,
		&pb.UpdateEventPayload{
			EventId: "evt-001",
			Patch:   map[string][]byte{"capacity": []byte("75")},
		},
		[]crypto.KeyPair{stewards[0], stewards[1], daveKP}, // alice + bob + dave (threshold 3)
	) {
		return
	}

	// ─── 6. RSVP — eve RSVPs to the event ───────────────────────────────
	// NOTE: in the current state machine, RSVP is steward-gated (every
	// non-CREATE_GROUP transition goes through VerifyStewardSignaturesLocked).
	// The proto comment says RSVP should be user-signed; that needs a
	// separate code path. Until that's wired, we sign with 3 stewards
	// (threshold after the CHANGE_THRESHOLD step).
	if !applyBroadcast(t, w, gkp, "RSVP eve (steward-signed; see note)",
		pb.TransitionType_TRANSITION_TYPE_RSVP,
		&pb.RsvpPayload{
			EventId: "evt-001",
			User:    evePB,
		},
		[]crypto.KeyPair{stewards[0], stewards[1], daveKP}, // 3 of 4 stewards (threshold 3)
	) {
		return
	}

	// ─── 7. CANCEL_EVENT ────────────────────────────────────────────────
	if !applyBroadcast(t, w, gkp, "CANCEL_EVENT",
		pb.TransitionType_TRANSITION_TYPE_CANCEL_EVENT,
		&pb.CancelEventPayload{
			EventId: "evt-001",
			Reason:  "venue double-booked",
		},
		[]crypto.KeyPair{stewards[0], stewards[1], daveKP}, // alice + bob + dave (threshold 3)
	) {
		return
	}

	// ─── 8. REMOVE_STEWARD — kick carol out ─────────────────────────────
	if !applyBroadcast(t, w, gkp, "REMOVE_STEWARD carol",
		pb.TransitionType_TRANSITION_TYPE_REMOVE_STEWARD,
		&pb.RemoveStewardPayload{Steward: &pb.PublicKey{Raw: stewards[2].Public[:]}},
		[]crypto.KeyPair{stewards[0], stewards[1], daveKP}, // alice + bob + dave (threshold 3)
	) {
		return
	}

	// ─── 9. REMOVE_MEMBER — eve out ────────────────────────────────────
	if !applyBroadcast(t, w, gkp, "REMOVE_MEMBER eve",
		pb.TransitionType_TRANSITION_TYPE_REMOVE_MEMBER,
		&pb.RemoveMemberPayload{User: evePB},
		[]crypto.KeyPair{stewards[0], stewards[1], daveKP}, // alice + bob + dave (threshold 3)
	) {
		return
	}

	// ─── convergence check ─────────────────────────────────────────────
	roots := make([]types.Hash, len(w.Hosts()))
	for i, h := range w.Hosts() {
		st := h.State(gkp.Public)
		if st == nil {
			t.Fatalf("host %s: no state for group", h.ID())
		}
		roots[i] = st.Root()
		t.Logf("host %s: state root = %x (log=%d)", h.ID(), roots[i], len(st.Log()))
	}

	reference := roots[0]
	for i := 1; i < len(roots); i++ {
		if roots[i] != reference {
			t.Errorf("host %s: state root = %x, want %x", w.Hosts()[i].ID(), roots[i], reference)
			continue
		}
	}

	// CREATE_GROUP + 9 lifecycle transitions = 10
	finalState := w.Hosts()[0].State(gkp.Public)
	t.Logf("final state has %d transitions in log", len(finalState.Log()))
	if got := len(finalState.Log()); got != 10 {
		t.Errorf("final state log size = %d, want 10", got)
	}

	t.Logf("group state root after full lifecycle: %x", reference)
}

// applyBroadcast builds, signs, and broadcasts a transition to all hosts.
// Returns true on success, false on failure (after logging the error).
//
// signWith are the keypairs whose signatures will be attached. They must
// satisfy the group's current threshold (caller keeps this in sync).
//
// innerPayload is the inner message (e.g. *pb.AddStewardPayload); the
// function wraps it in the right oneof variant for `kind`.
func applyBroadcast(
	t *testing.T,
	w *sim.World,
	gkp crypto.KeyPair,
	label string,
	kind pb.TransitionType,
	innerPayload interface{}, // one of the *pb.*Payload types
	signWith []crypto.KeyPair,
) bool {
	t.Helper()

	h0 := w.Hosts()[0]
	st0 := h0.State(gkp.Public)
	prior := st0.Root()
	priorRoot := &pb.StateRoot{Hash: prior[:]}

	trProto := &pb.Transition{
		Type:       kind,
		PriorState: priorRoot,
		SignedAt:   timestamppb.New(w.Now().Add(5 * time.Millisecond)),
	}

	// Set the oneof payload by type-switching on the inner type.
	// The wrappers are generated by protoc-gen-go and have form
	// `pb.Transition_*` (oneof variants).
	switch p := innerPayload.(type) {
	case *pb.CreateGroupPayload:
		trProto.Payload = &pb.Transition_CreateGroup{CreateGroup: p}
	case *pb.AddStewardPayload:
		trProto.Payload = &pb.Transition_AddSteward{AddSteward: p}
	case *pb.RemoveStewardPayload:
		trProto.Payload = &pb.Transition_RemoveSteward{RemoveSteward: p}
	case *pb.ChangeThresholdPayload:
		trProto.Payload = &pb.Transition_ChangeThreshold{ChangeThreshold: p}
	case *pb.AddMemberPayload:
		trProto.Payload = &pb.Transition_AddMember{AddMember: p}
	case *pb.RemoveMemberPayload:
		trProto.Payload = &pb.Transition_RemoveMember{RemoveMember: p}
	case *pb.CreateEventPayload:
		trProto.Payload = &pb.Transition_CreateEvent{CreateEvent: p}
	case *pb.UpdateEventPayload:
		trProto.Payload = &pb.Transition_UpdateEvent{UpdateEvent: p}
	case *pb.CancelEventPayload:
		trProto.Payload = &pb.Transition_CancelEvent{CancelEvent: p}
	case *pb.RsvpPayload:
		trProto.Payload = &pb.Transition_Rsvp{Rsvp: p}
	default:
		t.Fatalf("%s: unsupported inner payload type %T", label, innerPayload)
		return false
	}

	canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
	if err != nil {
		t.Fatalf("%s: marshal canonical: %v", label, err)
		return false
	}

	sigs := make([]*pb.Signature, 0, len(signWith))
	for _, k := range signWith {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	trProto.StewardSignatures = &pb.Multisig{
		Threshold:  uint32(len(signWith)),
		Signatures: sigs,
	}

	tr, err := group.NewTransition(trProto, gkp.Public)
	if err != nil {
		t.Fatalf("%s: NewTransition: %v", label, err)
		return false
	}
	tr.Proto.Hlc = hlc.New(w.Now().Add(10 * time.Millisecond))
	t.Logf("%s: prior=%x hlc=%x sigs=%d", label, priorRoot.GetHash()[:4], tr.Proto.GetHlc()[:8], len(signWith))

	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, tr); err != nil {
			t.Fatalf("%s on host %s: %v", label, h.ID(), err)
			return false
		}
	}

	w.Advance(50 * time.Millisecond)
	// Diagnostic: log post-state root on host[0]
	newRoot := w.Hosts()[0].State(gkp.Public).Root()
	t.Logf("%s: post_root=%x", label, newRoot[:4])
	return true
}

// keyPairFromSeed derives a deterministic 32-byte seed from the world's RNG
// using `label`, then constructs a KeyPair. Mirrors the seed-marshalling
// pattern in setupVegasProgrammers.
func keyPairFromSeed(w *sim.World, label string) crypto.KeyPair {
	seedU64 := w.DeriveSeed(label)
	var seed [32]byte
	for j := 0; j < 8; j++ {
		seed[j] = byte(seedU64 >> (8 * j))
	}
	return crypto.KeyPairFromSeed(seed)
}