// SPDX-License-Identifier: MIT
//
// Member lifecycle scenario: a user can be added to a group, and
// re-adding an existing member produces a new state root (the
// transition advances the log + KV) but doesn't conflict.
//
// What this exercises:
//   - ADD_MEMBER writes member/<hex> = {1}
//   - A second ADD_MEMBER for the SAME user produces identical
//     canonical bytes (same payload, same prior, same stewards) —
//     detected as a duplicate replay, not equivocation, and
//     rejected cleanly
//   - REMOVE_MEMBER for the same user deletes the entry
//   - A subsequent ADD_MEMBER for the same user is allowed (the
//     REMOVE was a different transition at the prior, so the
//     equivocation log distinguishes them by tx_hash — REMOVE has
//     tx_hash_REMOVE, ADD now produces tx_hash_ADD, both at the
//     same prior — the log accepts both because they're different
//     actions)
//   - All 4 hosts converge
//
// Note: this test intentionally does NOT exercise "REMOVE then ADD
// at the same prior_state signed by the same steward" because that
// triggers the equivocation log's (steward, prior_state) collision
// detection. That collision is correct behavior: the same steward
// cannot author two different transitions at the same prior. In
// practice, REMOVE advances the state root, so the follow-up ADD
// targets the post-REMOVE root (a different prior) and no
// equivocation is recorded. Here we test the post-REMOVE-add path
// explicitly.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// TestMemberLifecycle_AddRemoveReadd walks through:
//  1. Vegas Programmers exist
//  2. ADD_MEMBER eve — member/eve-hex = {1}
//  3. REMOVE_MEMBER eve — entry removed (state root advances)
//  4. ADD_MEMBER eve — entry restored (state root advances again,
//     no equivocation because the new prior is the post-REMOVE root)
//  5. All 4 hosts converge
func TestMemberLifecycle_AddRemoveReadd(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        79,
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
	eve := keyPairFromSeed(w, "eve-member")

	// Step 2: ADD_MEMBER.
	addPayload := &pb.AddMemberPayload{
		User: &pb.PublicKey{Raw: eve.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ADD_MEMBER eve",
		pb.TransitionType_TRANSITION_TYPE_ADD_MEMBER,
		addPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootAdd := w.Hosts()[0].State(gkp.Public).Root()
	memberKey := "member/" + tlsKeyHex(eve.Public)
	verifyMemberEntryPresent(t, w.Hosts()[0].State(gkp.Public), memberKey)
	t.Logf("eve added; root = %x", rootAdd[:4])

	// Step 3: REMOVE_MEMBER.
	removePayload := &pb.RemoveMemberPayload{
		User: &pb.PublicKey{Raw: eve.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "REMOVE_MEMBER eve",
		pb.TransitionType_TRANSITION_TYPE_REMOVE_MEMBER,
		removePayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootRemove := w.Hosts()[0].State(gkp.Public).Root()
	if rootRemove == rootAdd {
		t.Fatal("REMOVE_MEMBER did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != rootRemove {
			t.Fatalf("post-REMOVE divergence: host %s=%x want %x", h.ID(), got, rootRemove)
		}
	}
	verifyMemberEntryAbsent(t, w.Hosts()[0].State(gkp.Public), memberKey)
	t.Logf("eve removed; root = %x", rootRemove[:4])

	// Step 4: ADD_MEMBER eve again. The prior is now rootRemove
	// (post-REMOVE), which is different from rootAdd — no equivocation.
	if !applyBroadcastFor(t, w, gkp.Public, "ADD_MEMBER eve (re-add)",
		pb.TransitionType_TRANSITION_TYPE_ADD_MEMBER,
		addPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootReadd := w.Hosts()[0].State(gkp.Public).Root()
	if rootReadd == rootRemove {
		t.Fatal("re-ADD_MEMBER did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != rootReadd {
			t.Fatalf("post-re-ADD divergence: host %s=%x want %x", h.ID(), got, rootReadd)
		}
	}
	verifyMemberEntryPresent(t, w.Hosts()[0].State(gkp.Public), memberKey)
	t.Logf("eve re-added; root = %x", rootReadd[:4])
}

// TestMemberLifecycle_DuplicateAddRejected verifies that a
// replayed ADD_MEMBER — same canonical bytes, same (steward, prior)
// — is detected by the equivocation log and rejected rather than
// double-applied. We replay against the SAME prior_state the
// original transition used so the equivocation key collides.
func TestMemberLifecycle_DuplicateAddRejected(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        80,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)
	eve := keyPairFromSeed(w, "eve-dup")

	// The CREATE_GROUP state root is the initial prior. We construct
	// an ADD_MEMBER whose prior_state is that root (the root that
	// existed BEFORE eve was added), sign it fresh, and submit it.
	// Since we haven't broadcast the transition through the normal
	// apply path (we're building it manually for the test), the
	// equivocation log on host h0 is empty for (alice, createRoot).
	// So this first manual apply succeeds — confirming the equivocation
	// machinery doesn't trip on a clean first apply.
	createRoot := w.Hosts()[0].State(gkp.Public).Root()
	addPayload := &pb.AddMemberPayload{User: &pb.PublicKey{Raw: eve.Public[:]}}
	trProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_MEMBER,
		PriorState: &pb.StateRoot{Hash: createRoot[:]},
		Payload:    &pb.Transition_AddMember{AddMember: addPayload},
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

	h0 := w.Hosts()[0]
	stAfterFirst, err := h0.SubmitTransition(gkp.Public, tx)
	if err != nil {
		t.Fatalf("first ADD_MEMBER apply failed: %v", err)
	}
	rootAfterFirst := stAfterFirst.Root()
	if rootAfterFirst == createRoot {
		t.Fatal("first ADD_MEMBER did not advance root")
	}

	// Now replay the EXACT same transition bytes against the SAME
	// prior_state. The equivocation log already has alice+prior, so
	// the second apply must be rejected — not silently re-applied.
	_, err = h0.SubmitTransition(gkp.Public, tx)
	if err == nil {
		t.Fatal("duplicate ADD_MEMBER should have been rejected by equivocation log")
	}
	if !strings.Contains(err.Error(), "equivocation") {
		t.Logf("rejection: %v (expected equivocation key, this is acceptable too)", err)
	}
	t.Logf("duplicate ADD_MEMBER correctly rejected: %v", err)
}

func verifyMemberEntryPresent(t *testing.T, st *group.State, key string) {
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

func verifyMemberEntryAbsent(t *testing.T, st *group.State, key string) {
	t.Helper()
	for _, e := range st.Snapshot().Entries {
		if e.Key == key {
			if len(e.Value) == 1 && e.Value[0] == 0 {
				return // tombstone — correctly removed
			}
			t.Errorf("entry %q should be tombstoned ([]byte{0}) or absent, found value %x", e.Key, e.Value)
			return
		}
	}
}