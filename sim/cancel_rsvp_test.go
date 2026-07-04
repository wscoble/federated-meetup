// SPDX-License-Identifier: AGPL-3.0
//
// Cancel-RSVP scenario: a steward-signed CANCEL_RSVP removes a
// previously-recorded RSVP entry from the Merkle KV.
//
// What this exercises:
//   - CANCEL_RSVP: removes the rsvp/<event_id>/<user-hex> entry
//     (sets value to nil, which appendOrUpdate deletes)
//   - Cross-host convergence on the post-cancel transition
//   - The state root advances on cancel
//
// Design note: the proto comment for RsvpPayload says "user signs
// with their own key" — but the state machine enforces steward
// multisig on every transition. The protocol design is ambiguous
// here (see block 13 in cycle 0 of the design roadmap). The test
// exercises the current implementation (steward-gated) and is the
// right starting point; if the design moves to user-signed RSVPs
// this test would need to add a SignedEnvelope path.
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

// TestCancelRsvp_RemovesEntry walks through:
//  1. Vegas Programmers exist
//  2. Stewards create event "meetup-2026-07"
//  3. Eve RSVPs to that event (steward-gated)
//  4. Stewards CANCEL_RSVP for eve
//  5. rsvp/meetup-2026-07/<eve-hex> entry is removed
//  6. State root advances from each step
//  7. All 4 hosts converge
func TestCancelRsvp_RemovesEntry(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        55,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	mesh := sim.NewMesh(w, sim.DDILBenign)
	w.AttachMesh(mesh)

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)
	eve := keyPairFromSeed(w, "eve-attendee")

	// Pre-state.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-event: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}

	// Step 2: CREATE_EVENT.
	eventID := "meetup-2026-07"
	eventPayload := &pb.CreateEventPayload{
		EventId: eventID,
		Title:   "July Vegas Programmers Meetup",
		StartsAt: timestamppb.New(w.Now().Add(7 * 24 * time.Hour)),
		EndsAt:   timestamppb.New(w.Now().Add(7*24*time.Hour + 2*time.Hour)),
		Capacity: 50,
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CREATE_EVENT "+eventID,
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		eventPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	eventRoot := w.Hosts()[0].State(gkp.Public).Root()
	if eventRoot == parentRoot {
		t.Fatal("CREATE_EVENT did not advance root")
	}

	// Step 3: RSVP (steward-gated on eve's behalf).
	rsvpPayload := &pb.RsvpPayload{
		EventId: eventID,
		User:    &pb.PublicKey{Raw: eve.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "RSVP eve "+eventID,
		pb.TransitionType_TRANSITION_TYPE_RSVP,
		rsvpPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rsvpRoot := w.Hosts()[0].State(gkp.Public).Root()
	if rsvpRoot == eventRoot {
		t.Fatal("RSVP did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != rsvpRoot {
			t.Fatalf("post-RSVP divergence: host %s=%x want %x", h.ID(), got, rsvpRoot)
		}
	}
	rsvpKey := "rsvp/" + eventID + "/" + tlsKeyHex(eve.Public)
	verifyRsvpEntryPresent(t, w.Hosts()[0].State(gkp.Public), rsvpKey)
	t.Logf("RSVP recorded; root = %x", rsvpRoot[:4])

	// Step 4: CANCEL_RSVP.
	cancelPayload := &pb.CancelRsvpPayload{
		EventId: eventID,
		User:    &pb.PublicKey{Raw: eve.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CANCEL_RSVP eve "+eventID,
		pb.TransitionType_TRANSITION_TYPE_CANCEL_RSVP,
		cancelPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	cancelRoot := w.Hosts()[0].State(gkp.Public).Root()
	if cancelRoot == rsvpRoot {
		t.Fatal("CANCEL_RSVP did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != cancelRoot {
			t.Fatalf("post-CANCEL_RSVP divergence: host %s=%x want %x", h.ID(), got, cancelRoot)
		}
	}

	// Step 5: rsvp entry should be gone.
	verifyRsvpEntryAbsent(t, w.Hosts()[0].State(gkp.Public), rsvpKey)
	t.Logf("RSVP cancelled; root = %x", cancelRoot[:4])

	// Confirm event itself is still present.
	verifyEventEntryPresent(t, w.Hosts()[0].State(gkp.Public), eventID)
}

// verifyRsvpEntryPresent asserts the rsvp/<event>/<user-hex> entry
// exists with active-marker value []byte{1}.
func verifyRsvpEntryPresent(t *testing.T, st *group.State, key string) {
	t.Helper()
	for _, e := range st.Snapshot().Entries {
		if e.Key == key {
			if len(e.Value) != 1 || e.Value[0] != 1 {
				t.Errorf("entry %q present but value = %x (want []byte{1} = active)", key, e.Value)
			}
			return
		}
	}
	t.Errorf("entry %q not found in snapshot", key)
}

// verifyRsvpEntryAbsent asserts the rsvp/<event>/<user-hex> entry
// is tombstoned ([]byte{0}) or removed entirely. CANCEL_RSVP writes
// a tombstone so the post-cancel root is distinct from the pre-RSVP
// root, avoiding spurious equivocation on a subsequent re-RSVP.
func verifyRsvpEntryAbsent(t *testing.T, st *group.State, key string) {
	t.Helper()
	for _, e := range st.Snapshot().Entries {
		if e.Key == key {
			if len(e.Value) == 1 && e.Value[0] == 0 {
				return // tombstone — correctly cancelled
			}
			t.Errorf("entry %q should be tombstoned ([]byte{0}) or absent, found value %x", key, e.Value)
			return
		}
	}
}
// verifyEventEntryPresent asserts the event/<event_id> entry exists.
func verifyEventEntryPresent(t *testing.T, st *group.State, eventID string) {
	t.Helper()
	for _, e := range st.Snapshot().Entries {
		if e.Key == "event/"+eventID {
			return
		}
	}
	t.Errorf("event entry %q not found in snapshot", "event/"+eventID)
}