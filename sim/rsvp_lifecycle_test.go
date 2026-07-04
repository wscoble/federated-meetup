// SPDX-License-Identifier: AGPL-3.0
//
// RSVP lifecycle: a user can RSVP to an event, cancel, then RSVP
// again. Like member-lifecycle (cycle 25), this exercises a
// delete-then-readd path that previously caused the Merkle KV to
// collapse, making the second RSVP collide with an earlier prior
// in the equivocation log.
package sim_test

import (
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRsvpLifecycle_AddCancelAdd(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        91,
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
	eve := keyPairFromSeed(w, "eve-rsvp")

	// First we need an event to RSVP to.
	createEvt := &pb.CreateEventPayload{
		EventId:  "kickoff-2026-q3",
		Title:    "Q3 Kickoff",
		StartsAt: timestamppb.New(time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)),
		Capacity: 100,
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CREATE_EVENT",
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		createEvt,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}

	// RSVP #1.
	rsvpPayload := &pb.RsvpPayload{
		EventId: "kickoff-2026-q3",
		User:    &pb.PublicKey{Raw: eve.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "RSVP yes",
		pb.TransitionType_TRANSITION_TYPE_RSVP,
		rsvpPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootRsvp1 := w.Hosts()[0].State(gkp.Public).Root()

	// Cancel RSVP.
	cancelPayload := &pb.CancelRsvpPayload{
		EventId: "kickoff-2026-q3",
		User:    &pb.PublicKey{Raw: eve.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CANCEL_RSVP",
		pb.TransitionType_TRANSITION_TYPE_CANCEL_RSVP,
		cancelPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootCancel := w.Hosts()[0].State(gkp.Public).Root()
	if rootCancel == rootRsvp1 {
		t.Fatal("CANCEL_RSVP did not advance root")
	}

	// RSVP again. Must not trigger spurious equivocation detection.
	if !applyBroadcastFor(t, w, gkp.Public, "RSVP yes (re-add)",
		pb.TransitionType_TRANSITION_TYPE_RSVP,
		rsvpPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootRsvp2 := w.Hosts()[0].State(gkp.Public).Root()
	if rootRsvp2 == rootCancel {
		t.Fatal("re-RSVP did not advance root")
	}

	// All hosts must converge on the post-re-RSVP root.
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != rootRsvp2 {
			t.Fatalf("post-re-RSVP divergence: host %s=%x want %x", h.ID(), got, rootRsvp2)
		}
	}
}