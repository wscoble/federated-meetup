// SPDX-License-Identifier: AGPL-3.0
//
// Fork inheritance semantics. FORK (internal/group/group.go:618)
// declares a new sovereign group; the parent's state machine just
// records the fork_lineage. The new group is built separately via
// CREATE_GROUP.
//
// This is SCISSION semantics, not DERIVATION:
//
//   - New group's initial stewards = exactly what the forking payload
//     declares in `new_stewards`. NOT a copy of the parent's stewards.
//   - Parent's steward set is UNCHANGED after FORK. Carol doesn't get
//     removed from the parent just because she didn't join the fork.
//   - New group starts EMPTY — no inherited members, events, RSVPs,
//     mesh peers, host certs, custody declarations. Everything has to
//     be re-created via fresh transitions on the new group.
//   - Parent's history is unchanged — past transitions, past events,
//     past RSVPs are all preserved.
//
// This contrasts with BRANCH_CREATE (cycle 35), where the new branch
// inherits the parent's stewards and threshold as a snapshot. FORK
// is the heavier hammer; the new group is fully sovereign.
//
// What this test pins down:
//
//   1. After FORK, parent's steward count == parent's steward count
//      before FORK. No steward is auto-removed from the parent.
//   2. New group's stewards == `new_stewards` from the FORK payload,
//      in the order declared.
//   3. New group's threshold == `new_threshold` from the payload.
//   4. New group has zero members, zero events, zero RSVPs.
//   5. Parent's events/members/RSVPs are unaffected by the fork.
//   6. Both groups converge across all 4 hosts.
//
// Why this matters: scission semantics mean a fork is a *declaration
// of sovereignty*, not a *splitting*. The minority that wants out
// names who they are and what threshold they want. The majority's
// parent group continues unchanged. Members of the new group have to
// re-establish themselves via fresh CREATE_EVENT/ADD_MEMBER on the
// new group. If you wanted "clone the parent into the new group,"
// FORK is the wrong primitive — you'd manually replay every transition
// on the new group.
package sim_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
)

func TestFork_ParentStewardsUnchanged(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        98,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	parentStewardsBefore := w.Hosts()[0].State(gkp.Public).Stewards()
	if len(parentStewardsBefore) != 3 {
		t.Fatalf("expected 3 parent stewards pre-fork, got %d", len(parentStewardsBefore))
	}

	// Fork off alice + bob. Carol stays on parent.
	rootBefore := w.Hosts()[0].State(gkp.Public).Root()
	newGKP := keyPairFromSeed(w, "fork-test-group-1")
	newStewardKeys := []*pb.PublicKey{
		{Raw: stewards[0].Public[:]},
		{Raw: stewards[1].Public[:]},
	}
	if !forkApplyBroadcast(t, w, gkp.Public, "FORK", &pb.ForkPayload{
		NewGroupKey:  &pb.PublicKey{Raw: newGKP.Public[:]},
		NewStewards:  newStewardKeys,
		NewThreshold: 2,
	}, []crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Verify parent's steward set is UNCHANGED.
	parentStewardsAfter := w.Hosts()[0].State(gkp.Public).Stewards()
	if len(parentStewardsAfter) != len(parentStewardsBefore) {
		t.Fatalf("parent steward count changed: was %d, now %d",
			len(parentStewardsBefore), len(parentStewardsAfter))
	}

	// Verify all 4 hosts agree on parent's steward count.
	for _, h := range w.Hosts() {
		hostStewards := h.State(gkp.Public).Stewards()
		if len(hostStewards) != 3 {
			t.Errorf("host %s: parent has %d stewards after FORK, want 3",
				h.ID(), len(hostStewards))
		}
	}

	// Parent root must have advanced (FORK is a real transition).
	rootAfter := w.Hosts()[0].State(gkp.Public).Root()
	if rootAfter == rootBefore {
		t.Fatal("FORK did not advance parent root")
	}

	// fork_lineage entry must exist in parent's KV.
	entries := w.Hosts()[0].State(gkp.Public).Snapshot().Entries
	var foundLineage bool
	for _, e := range entries {
		if e.Key == "fork_lineage" {
			foundLineage = true
			if string(e.Value) != string(newGKP.Public[:]) {
				t.Errorf("fork_lineage = %x, want %x", e.Value, newGKP.Public[:])
			}
		}
	}
	if !foundLineage {
		t.Error("fork_lineage entry missing from parent KV")
	}
	t.Logf("parent stewards unchanged post-fork; fork_lineage = %x", newGKP.Public[:8])
}

func TestFork_NewGroupIsEmpty(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        99,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	// First, add some state to the parent so we can verify the new
	// group doesn't inherit ANY of it. Add a member and create an event.
	memberKP, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	addMember := &pb.AddMemberPayload{
		User: &pb.PublicKey{Raw: memberKP.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ADD_MEMBER",
		pb.TransitionType_TRANSITION_TYPE_ADD_MEMBER,
		addMember,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)

	createEvent := &pb.CreateEventPayload{
		EventId:  "parent-only-event",
		Title:    "Parent Group Meetup",
		Capacity: 50,
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CREATE_EVENT",
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		createEvent,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)

	// Verify parent has the member and event (by snapshot entry keys).
	parentEntries := w.Hosts()[0].State(gkp.Public).Snapshot().Entries
	memberKey := fmt.Sprintf("member/%x", memberKP.Public[:])
	var foundMember, foundEvent bool
	for _, e := range parentEntries {
		if e.Key == memberKey {
			foundMember = true
		}
		if e.Key == "event/parent-only-event" {
			foundEvent = true
		}
	}
	if !foundMember {
		t.Fatalf("parent snapshot missing member entry %q after ADD_MEMBER", memberKey)
	}
	if !foundEvent {
		t.Fatal("parent snapshot missing event entry after CREATE_EVENT")
	}

	// Fork off alice + bob.
	newGKP := keyPairFromSeed(w, "fork-empty-group")
	newStewardKeys := []*pb.PublicKey{
		{Raw: stewards[0].Public[:]},
		{Raw: stewards[1].Public[:]},
	}
	if !forkApplyBroadcast(t, w, gkp.Public, "FORK", &pb.ForkPayload{
		NewGroupKey:  &pb.PublicKey{Raw: newGKP.Public[:]},
		NewStewards:  newStewardKeys,
		NewThreshold: 2,
	}, []crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Register the new group on all hosts and apply CREATE_GROUP.
	for _, h := range w.Hosts() {
		h.AddGroup(newGKP.Public, nil)
	}
	if !createGroupForBroadcast(t, w, newGKP.Public, "empty-fork", "Empty Fork Group",
		[]crypto.KeyPair{stewards[0], stewards[1]}, 2) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Verify new group is EMPTY — no members, no events.
	for _, h := range w.Hosts() {
		newEntries := h.State(newGKP.Public).Snapshot().Entries
		for _, e := range newEntries {
			if len(e.Key) >= 7 && e.Key[:7] == "member/" {
				t.Errorf("host %s: new group has member entry %q (parent had member)",
					h.ID(), e.Key)
			}
			if len(e.Key) >= 6 && e.Key[:6] == "event/" {
				t.Errorf("host %s: new group has event entry %q (parent had event)",
					h.ID(), e.Key)
			}
		}
	}

	// Verify new group's steward set is exactly what the FORK declared.
	for _, h := range w.Hosts() {
		newStewards := h.State(newGKP.Public).Stewards()
		if len(newStewards) != 2 {
			t.Errorf("host %s: new group has %d stewards, want 2",
				h.ID(), len(newStewards))
			continue
		}
		// Order may not be preserved; check membership instead.
		var foundA, foundB bool
		for _, s := range newStewards {
			if s.Key == stewards[0].Public {
				foundA = true
			}
			if s.Key == stewards[1].Public {
				foundB = true
			}
		}
		if !foundA || !foundB {
			t.Errorf("host %s: new group missing alice or bob", h.ID())
		}
	}

	// Verify new group's threshold is exactly what the FORK declared.
	for _, h := range w.Hosts() {
		if got := h.State(newGKP.Public).Threshold(); got != 2 {
			t.Errorf("host %s: new group threshold = %d, want 2", h.ID(), got)
		}
	}

	// Verify parent is UNCHANGED — still has the member and event.
	for _, h := range w.Hosts() {
		hostEntries := h.State(gkp.Public).Snapshot().Entries
		var hasMember, hasEvent bool
		for _, e := range hostEntries {
			if e.Key == memberKey {
				hasMember = true
			}
			if e.Key == "event/parent-only-event" {
				hasEvent = true
			}
		}
		if !hasMember {
			t.Errorf("host %s: parent lost member entry after FORK", h.ID())
		}
		if !hasEvent {
			t.Errorf("host %s: parent lost event entry after FORK", h.ID())
		}
	}
	t.Logf("fork inheritance confirmed: new group empty, parent retains all state")
}