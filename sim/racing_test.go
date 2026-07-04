// SPDX-License-Identifier: AGPL-3.0
//
// Racing transitions: two transitions submitted at the same
// prior_state. Only one can apply (the state root advances
// monotonically). The other is rejected with prior_state mismatch
// (the second submission sees the post-first root, not its
// expected prior).
//
// What this exercises:
//   - Host h0 accepts the first transition (CREATE_EVENT meetup-A)
//   - Host h0 rejects the second transition (CREATE_EVENT meetup-B)
//     because its prior_state no longer matches (post-A root != pre-A)
//   - The second transition is rejected with prior_state mismatch
//   - State advances exactly once
//
// Why this matters: this is how consensus protects against
// double-spend / conflicting transitions. Even if a malicious
// steward crafts two transitions at the same prior_state and
// broadcasts both, the protocol's serial apply path means only
// one applies. The other is rejected and the rejected transition
// remains visible in the dropped-messages counter for audit.
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

// TestRacingTransitions_OnlyOneApplies walks through:
//  1. Vegas Programmers exist on h0
//  2. Build two CREATE_EVENT transitions, both with prior=parentRoot,
//     both signed by the threshold
//  3. Submit the first to h0: succeeds, root advances
//  4. Submit the second to h0 with the SAME prior=parentRoot: rejected
//     (prior_state mismatch — h0's current root != parentRoot anymore)
//  5. State has both event/A in the snapshot, but NOT event/B
//  6. The rejection is visible (err contains "prior")
func TestRacingTransitions_OnlyOneApplies(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        76,
		HostCount:   1, // single host to isolate the race
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))
	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	h0 := w.Hosts()[0]
	parentRoot := h0.State(gkp.Public).Root()

	// Build two competing CREATE_EVENT transitions at the same prior.
	buildEvent := func(eventID, title string) *group.Transition {
		payload := &pb.CreateEventPayload{
			EventId:  eventID,
			Title:    title,
			StartsAt: timestamppb.New(w.Now().Add(7 * 24 * time.Hour)),
			EndsAt:   timestamppb.New(w.Now().Add(7*24*time.Hour + 2*time.Hour)),
			Capacity: 50,
		}
		trProto := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
			PriorState: &pb.StateRoot{Hash: parentRoot[:]},
			Payload:    &pb.Transition_CreateEvent{CreateEvent: payload},
		}
		canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
		if err != nil {
			t.Fatal(err)
		}
		sigs := []*pb.Signature{}
		for _, k := range []crypto.KeyPair{stewards[0], stewards[1]} {
			s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
			sigs = append(sigs, &pb.Signature{Raw: s[:]})
		}
		trProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
		tx, err := group.NewTransition(trProto, gkp.Public)
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}

	trA := buildEvent("meetup-A", "Race Winner")
	trB := buildEvent("meetup-B", "Race Loser")

	// Step 3: submit A. Should succeed.
	if _, err := h0.SubmitTransition(gkp.Public, trA); err != nil {
		t.Fatalf("first transition should have applied: %v", err)
	}
	rootAfterA := h0.State(gkp.Public).Root()
	if rootAfterA == parentRoot {
		t.Fatal("first transition did not advance root")
	}
	t.Logf("meetup-A applied; root = %x", rootAfterA[:4])

	// Step 4: submit B with the same prior_state. Should reject.
	_, err = h0.SubmitTransition(gkp.Public, trB)
	if err == nil {
		t.Fatal("second transition should have been rejected (prior_state mismatch)")
	}
	if !strings.Contains(err.Error(), "prior") {
		t.Errorf("rejection should mention prior_state, got: %v", err)
	}
	t.Logf("meetup-B correctly rejected: %v", err)

	// Step 5: snapshot has event/meetup-A but not event/meetup-B.
	foundA, foundB := false, false
	for _, e := range h0.State(gkp.Public).Snapshot().Entries {
		if e.Key == "event/meetup-A" && e.Value != nil {
			foundA = true
		}
		if e.Key == "event/meetup-B" {
			foundB = true
		}
	}
	if !foundA {
		t.Error("event/meetup-A should be present (winner)")
	}
	if foundB {
		t.Error("event/meetup-B should NOT be present (loser rejected)")
	}
}