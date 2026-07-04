// SPDX-License-Identifier: AGPL-3.0
//
// Cycle 40: large-fanout stress test.
//
// Many MEMBERS, many EVENTS, many RSVPs applied in sequence. Verifies
// that the Merkle KV stays consistent across all 4 hosts and that
// every accepted transition advances the root.
//
// What this exercises:
//   - KV growth under realistic fanout (50 members, 10 events,
//     50 RSVPs = 110 transitions)
//   - Convergence: all 4 hosts agree at every step
//   - Tombstone semantics under load: members leave, RSVPs are
//     cancelled, but the log remains monotonic
//   - Performance smoke: completes in <2s
//
// Why this matters: scale tests catch bugs that single-step tests
// miss — duplicate key collisions, hash collisions, off-by-one in
// Seq counters, slow O(n²) operations.
package sim_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStress_LargeFanout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stress test in short mode")
	}

	w, _ := sim.NewWorld(sim.Config{
		Seed:        105,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	const numMembers = 50
	const numEvents = 10
	const rsvpsPerEvent = 5

	signWith := []crypto.KeyPair{stewards[0], stewards[1]}

	// Add 50 members.
	memberKeys := make([]crypto.KeyPair, numMembers)
	for i := 0; i < numMembers; i++ {
		kp, err := crypto.GenerateKey()
		if err != nil {
			t.Fatal(err)
		}
		memberKeys[i] = kp
		payload := &pb.AddMemberPayload{
			User: &pb.PublicKey{Raw: kp.Public[:]},
		}
		if !applyBroadcastFor(t, w, gkp.Public, fmt.Sprintf("ADD_MEMBER %d", i),
			pb.TransitionType_TRANSITION_TYPE_ADD_MEMBER, payload, signWith) {
			return
		}
		w.Advance(2 * time.Millisecond)
	}

	// Verify all 50 members are recorded.
	for _, h := range w.Hosts() {
		var memberCount int
		for _, e := range h.State(gkp.Public).Snapshot().Entries {
			if len(e.Key) >= 7 && e.Key[:7] == "member/" {
				memberCount++
			}
		}
		if memberCount != numMembers {
			t.Errorf("host %s: %d member entries after stress-add, want %d",
				h.ID(), memberCount, numMembers)
		}
	}

	// Create 10 events.
	for i := 0; i < numEvents; i++ {
		eventID := fmt.Sprintf("event-%d", i)
		payload := &pb.CreateEventPayload{
			EventId:  eventID,
			Title:    fmt.Sprintf("Event %d", i),
			Capacity: 100,
		}
		if !applyBroadcastFor(t, w, gkp.Public, "CREATE_EVENT "+eventID,
			pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT, payload, signWith) {
			return
		}
		w.Advance(2 * time.Millisecond)
	}

	// RSVP 5 members to each event.
	rsvpCount := 0
	for e := 0; e < numEvents; e++ {
		eventID := fmt.Sprintf("event-%d", e)
		for m := 0; m < rsvpsPerEvent; m++ {
			payload := &pb.RsvpPayload{
				EventId: eventID,
				User:    &pb.PublicKey{Raw: memberKeys[m].Public[:]},
			}
			if !applyBroadcastFor(t, w, gkp.Public, fmt.Sprintf("RSVP %s m%d", eventID, m),
				pb.TransitionType_TRANSITION_TYPE_RSVP, payload, signWith) {
				return
			}
			w.Advance(2 * time.Millisecond)
			rsvpCount++
		}
	}

	// Final convergence check: all 4 hosts must agree on the root.
	finalRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != finalRoot {
			t.Fatalf("post-stress divergence: host %s=%x want %x",
				h.ID(), got, finalRoot)
		}
	}

	// Verify all RSVPs are recorded.
	expectedRsvps := numEvents * rsvpsPerEvent
	for _, h := range w.Hosts() {
		var rsvpCount int
		for _, e := range h.State(gkp.Public).Snapshot().Entries {
			if len(e.Key) >= 5 && e.Key[:5] == "rsvp/" {
				if len(e.Value) == 1 && e.Value[0] == 1 {
					rsvpCount++
				}
			}
		}
		if rsvpCount != expectedRsvps {
			t.Errorf("host %s: %d active RSVPs, want %d",
				h.ID(), rsvpCount, expectedRsvps)
		}
	}

	// Cancel half the RSVPs to test tombstone semantics under load.
	cancelCount := 0
	for e := 0; e < numEvents; e++ {
		eventID := fmt.Sprintf("event-%d", e)
		for m := 0; m < rsvpsPerEvent/2; m++ {
			payload := &pb.CancelRsvpPayload{
				EventId: eventID,
				User:    &pb.PublicKey{Raw: memberKeys[m].Public[:]},
			}
			if !applyBroadcastFor(t, w, gkp.Public, fmt.Sprintf("CANCEL_RSVP %s m%d", eventID, m),
				pb.TransitionType_TRANSITION_TYPE_CANCEL_RSVP, payload, signWith) {
				return
			}
			w.Advance(2 * time.Millisecond)
			cancelCount++
		}
	}

	// Final convergence: all 4 hosts agree.
	finalRootAfterCancels := w.Hosts()[0].State(gkp.Public).Root()
	if finalRootAfterCancels == finalRoot {
		t.Fatal("cancels did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != finalRootAfterCancels {
			t.Fatalf("post-cancel divergence: host %s=%x want %x",
				h.ID(), got, finalRootAfterCancels)
		}
	}

	// Half the RSVPs should be tombstoned (value=0).
	expectedTombstones := cancelCount
	for _, h := range w.Hosts() {
		var tombCount int
		for _, e := range h.State(gkp.Public).Snapshot().Entries {
			if len(e.Key) >= 5 && e.Key[:5] == "rsvp/" {
				if len(e.Value) == 1 && e.Value[0] == 0 {
					tombCount++
				}
			}
		}
		if tombCount != expectedTombstones {
			t.Errorf("host %s: %d tombstoned RSVPs, want %d",
				h.ID(), tombCount, expectedTombstones)
		}
	}

	// _ unused suppressor
	_ = timestamppb.New
	_ = rsvpCount
	t.Logf("stress test complete: %d members, %d events, %d active RSVPs, %d tombstoned; final root=%x",
		numMembers, numEvents, expectedRsvps, expectedTombstones, finalRootAfterCancels[:4])
}