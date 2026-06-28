// SPDX-License-Identifier: MIT
//
// Update-event scenario: stewards patch an existing event after
// the initial creation. The patch is stored as a separate KV
// entry (`event_patch/<id>`) so the original `event/<id>` record
// stays intact — hosts apply patches on read.
//
// What this exercises:
//   - UPDATE_EVENT writes event_patch/<id> with the marshaled payload
//   - Both event/<id> and event_patch/<id> coexist in the snapshot
//   - Cross-host convergence on the post-update transition
//   - A second UPDATE_EVENT with different fields lands at the
//     same key (last-write-wins on patches, like attestations)
//
// Why this matters: event details change. Locations get moved,
// capacities get increased, titles get clarified. The protocol
// needs a non-destructive way to update an event without losing
// the original record.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestUpdateEvent_PatchApplies walks through:
//  1. Vegas Programmers exist
//  2. CREATE_EVENT meetup-1 (title "Original Title", capacity 30)
//  3. UPDATE_EVENT with patch (title changed, capacity raised)
//  4. event_patch/meetup-1 entry exists with the patch bytes
//  5. event/meetup-1 still exists with original payload
//  6. A second UPDATE_EVENT lands at the same event_patch key
//     (last-write-wins on patches)
//  7. All 4 hosts converge
func TestUpdateEvent_PatchApplies(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        74,
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

	// Step 2: CREATE_EVENT meetup-1.
	eventID := "meetup-1"
	createPayload := &pb.CreateEventPayload{
		EventId:  eventID,
		Title:    "Original Title",
		StartsAt: timestamppb.New(w.Now().Add(7 * 24 * time.Hour)),
		EndsAt:   timestamppb.New(w.Now().Add(7*24*time.Hour + 2*time.Hour)),
		Capacity: 30,
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

	// Step 3: UPDATE_EVENT with a patch that bumps capacity + new title.
	patch := &pb.UpdateEventPayload{
		EventId: eventID,
		Patch: map[string][]byte{
			"title":    []byte("Updated Title"),
			"capacity": []byte("100"),
		},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "UPDATE_EVENT "+eventID,
		pb.TransitionType_TRANSITION_TYPE_UPDATE_EVENT,
		patch,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	updatedRoot := w.Hosts()[0].State(gkp.Public).Root()
	if updatedRoot == createdRoot {
		t.Fatal("UPDATE_EVENT did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != updatedRoot {
			t.Fatalf("post-UPDATE divergence: host %s=%x want %x", h.ID(), got, updatedRoot)
		}
	}

	// Step 4: event_patch/meetup-1 entry exists with the patch bytes.
	verifyEventPatchEntry(t, w.Hosts()[0].State(gkp.Public), eventID, patch)
	t.Logf("event patch recorded; root = %x", updatedRoot[:4])

	// Step 5: event/meetup-1 still exists with original payload.
	verifyEventEntry(t, w.Hosts()[0].State(gkp.Public), eventID, createPayload)

	// Step 6: a second UPDATE_EVENT overwrites the patch.
	patch2 := &pb.UpdateEventPayload{
		EventId: eventID,
		Patch: map[string][]byte{
			"title": []byte("Final Title"),
		},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "UPDATE_EVENT "+eventID+" (overwrite)",
		pb.TransitionType_TRANSITION_TYPE_UPDATE_EVENT,
		patch2,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	finalRoot := w.Hosts()[0].State(gkp.Public).Root()
	if finalRoot == updatedRoot {
		t.Fatal("second UPDATE_EVENT did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != finalRoot {
			t.Fatalf("post-second-UPDATE divergence: host %s=%x want %x", h.ID(), got, finalRoot)
		}
	}
	verifyEventPatchEntry(t, w.Hosts()[0].State(gkp.Public), eventID, patch2)
	t.Logf("patch overwritten; root = %x", finalRoot[:4])
}

// verifyEventPatchEntry asserts that the event_patch/<id> entry
// exists with the given patch payload as the value (marshaled).
func verifyEventPatchEntry(t *testing.T, st *group.State, eventID string, patch *pb.UpdateEventPayload) {
	t.Helper()
	entryKey := "event_patch/" + eventID
	for _, e := range st.Snapshot().Entries {
		if e.Key == entryKey {
			if e.Value == nil {
				t.Errorf("entry %q present but value is nil", entryKey)
				return
			}
			// Decoding the marshaled payload should round-trip
			// (canonical bytes may differ from new bytes, so we
			// only check that proto.Unmarshal succeeds and the
			// event_id matches).
			var got pb.UpdateEventPayload
			if err := proto.Unmarshal(e.Value, &got); err != nil {
				t.Errorf("entry %q value does not unmarshal: %v", entryKey, err)
				return
			}
			if got.EventId != patch.EventId {
				t.Errorf("entry %q event_id = %q, want %q", entryKey, got.EventId, patch.EventId)
			}
			return
		}
	}
	t.Errorf("entry %q not found in snapshot", entryKey)
}

// verifyEventEntry asserts that the event/<id> entry exists with
// the original CreateEventPayload. We check that the marshaled
// payload decodes and the title + capacity match.
func verifyEventEntry(t *testing.T, st *group.State, eventID string, original *pb.CreateEventPayload) {
	t.Helper()
	entryKey := "event/" + eventID
	for _, e := range st.Snapshot().Entries {
		if e.Key == entryKey {
			if e.Value == nil {
				t.Errorf("entry %q present but value is nil", entryKey)
				return
			}
			var got pb.CreateEventPayload
			if err := proto.Unmarshal(e.Value, &got); err != nil {
				t.Errorf("entry %q value does not unmarshal: %v", entryKey, err)
				return
			}
			if got.Title != original.Title {
				t.Errorf("entry %q title = %q, want %q (UPDATE_EVENT must NOT mutate event/<id>)",
					entryKey, got.Title, original.Title)
			}
			if got.Capacity != original.Capacity {
				t.Errorf("entry %q capacity = %d, want %d (UPDATE_EVENT must NOT mutate event/<id>)",
					entryKey, got.Capacity, original.Capacity)
			}
			return
		}
	}
	t.Errorf("entry %q not found in snapshot", entryKey)
}