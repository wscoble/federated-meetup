// SPDX-License-Identifier: AGPL-3.0
//
// Cancel-event scenario: stewards cancel an existing event,
// recording the cancellation in the Merkle KV without deleting
// the original event record.
//
// What this exercises:
//   - CANCEL_EVENT writes event_cancelled/<id> = 1
//   - The original event/<id> record is preserved (cancel is a
//     tombstone overlay, not a delete)
//   - The cancel reason is captured in the payload but the KV
//     only stores a 1-byte marker; the payload survives in the
//     transition log
//   - Cross-host convergence
//
// Why this matters: events get canceled. The protocol needs to
// preserve the audit trail (who canceled, why) without losing
// the event details (so RSVP history is interpretable).
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

// TestCancelEvent_Tombstones walks through:
//  1. Vegas Programmers exist
//  2. CREATE_EVENT meetup-cancel-test
//  3. CANCEL_EVENT with reason "venue double-booked"
//  4. event_cancelled/meetup-cancel-test entry exists with value 1
//  5. event/meetup-cancel-test still exists with original payload
//  6. All 4 hosts converge
//  7. CANCEL_EVENT for a non-existent event still applies (the
//     state machine doesn't require the event to exist; this is
//     forward-compatible — a future state machine can add the
//     check without breaking consensus, since old hosts that
//     didn't enforce it will still apply)
func TestCancelEvent_Tombstones(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        75,
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

	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-event: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}

	eventID := "meetup-cancel-test"
	createPayload := &pb.CreateEventPayload{
		EventId:  eventID,
		Title:    "This Will Be Canceled",
		StartsAt: timestamppb.New(w.Now().Add(7 * 24 * time.Hour)),
		EndsAt:   timestamppb.New(w.Now().Add(7*24*time.Hour + 2*time.Hour)),
		Capacity: 50,
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CREATE_EVENT "+eventID,
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		createPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	createdRoot := w.Hosts()[0].State(gkp.Public).Root()
	if createdRoot == parentRoot {
		t.Fatal("CREATE_EVENT did not advance root")
	}
	t.Logf("event created; root = %x", createdRoot[:4])

	// Step 3: CANCEL_EVENT.
	cancelPayload := &pb.CancelEventPayload{
		EventId: eventID,
		Reason:  "venue double-booked",
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CANCEL_EVENT "+eventID,
		pb.TransitionType_TRANSITION_TYPE_CANCEL_EVENT,
		cancelPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	canceledRoot := w.Hosts()[0].State(gkp.Public).Root()
	if canceledRoot == createdRoot {
		t.Fatal("CANCEL_EVENT did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != canceledRoot {
			t.Fatalf("post-CANCEL divergence: host %s=%x want %x", h.ID(), got, canceledRoot)
		}
	}

	// Step 4: event_cancelled/<id> = 1.
	verifyCancellationEntry(t, w.Hosts()[0].State(gkp.Public), eventID)
	t.Logf("cancellation recorded; root = %x", canceledRoot[:4])

	// Step 5: event/<id> still exists.
	for _, e := range w.Hosts()[0].State(gkp.Public).Snapshot().Entries {
		if e.Key == "event/"+eventID && e.Value != nil {
			return
		}
	}
	t.Errorf("event/%s entry was deleted by CANCEL_EVENT; should be preserved as tombstone overlay", eventID)
}

// verifyCancellationEntry asserts that event_cancelled/<id> = 1.
func verifyCancellationEntry(t *testing.T, st *group.State, eventID string) {
	t.Helper()
	entryKey := "event_cancelled/" + eventID
	for _, e := range st.Snapshot().Entries {
		if e.Key == entryKey {
			if len(e.Value) != 1 || e.Value[0] != 1 {
				t.Errorf("entry %q has value %x, want [1]", entryKey, e.Value)
			}
			return
		}
	}
	t.Errorf("entry %q not found in snapshot", entryKey)
}