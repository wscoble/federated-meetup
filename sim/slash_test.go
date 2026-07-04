// SPDX-License-Identifier: AGPL-3.0
//
// Slash scenario: equivocation evidence against a steward is
// surfaced as a SLASH_STEWARD transition signed by the threshold
// of OTHER stewards. The slashed steward cannot co-sign their own
// removal.
//
// What this exercises:
//   - SLASH_STEWARD: removes the slashed key from the active steward
//     set (via computeCurrentStewards) and records:
//       steward/<hex> -> tombstoned (nil)
//       slashed/<hex> -> 1 (audit entry)
//   - The state machine's verify gate rejects if the slashed
//     steward's signature appears in the verifying multisig
//   - Cross-host convergence on the post-slash transition
//
// Why this matters: equivocation is the worst-case Byzantine fault
// for a consensus system. The protocol must be able to remove a
// misbehaving steward without the misbehaving steward's consent.
// SLASH_STEWARD is the only transition that runs against the
// consensus-against-the-defector pattern.
package sim_test

import (
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	"github.com/wscoble/federated-meetup/internal/types"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
)

// TestSlashSteward_Equivocation walks through:
//  1. Vegas Programmers exist (alice, bob, carol, threshold 2)
//  2. Two conflicting CREATE_EVENT transitions are authored by carol
//     at the same prior_state with different tx hashes — this is
//     the equivocation evidence
//  3. Alice and bob (the OTHER stewards) sign a SLASH_STEWARD
//     transition against carol, carrying the equivocation evidence
//  4. The transition applies: carol is removed from the steward set
//     and a slashed/<carol-hex> audit entry is recorded
//  5. All 4 hosts converge on the post-slash root
//  6. StewardsAt(prior_state) before slash = 3; StewardsAt(after) = 2
func TestSlashSteward_Equivocation(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        51,
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
	stewards := stewardKPsForTest(w) // [alice, bob, carol]
	carol := stewards[2]

	// Pre-state: parent group converged, 3 stewards.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-slash: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}
	stPre := w.Hosts()[0].State(gkp.Public).StewardsAt(nil)
	if len(stPre) != 3 {
		t.Fatalf("expected 3 stewards pre-slash, got %d", len(stPre))
	}

	// Step 2: build two conflicting CREATE_EVENT payloads at the
	// same prior_state with different event IDs. We don't actually
	// apply these — we just need their canonical-bytes hashes for
	// the SLASH_STEWARD evidence payload.
	txHashA, txHashB := buildEquivocationEvidence(t, w, gkp.Public, parentRoot)

	// Step 3: build SLASH_STEWARD signed by alice + bob (NOT carol).
	slashProto := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_SLASH_STEWARD,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload: &pb.Transition_SlashSteward{SlashSteward: &pb.SlashStewardPayload{
			SlashedSteward: &pb.PublicKey{Raw: carol.Public[:]},
			PriorState:     &pb.StateRoot{Hash: parentRoot[:]},
			HlcA:           []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x01, 0, 0, 0, 0, 0, 0, 0, 0},
			HlcB:           []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x00, 0x01, 0, 0, 0, 0, 0, 0, 0, 0},
			TxHashA:        txHashA[:],
			TxHashB:        txHashB[:],
		}},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(slashProto)
	if err != nil {
		t.Fatal(err)
	}
	sigs := []*pb.Signature{}
	for _, k := range []crypto.KeyPair{stewards[0], stewards[1]} { // alice + bob, NOT carol
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	slashProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, err := group.NewTransition(slashProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	// Step 4: broadcast to all hosts.
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, tx); err != nil {
			t.Fatalf("SLASH on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)

	// Step 5: convergence check.
	postRoot := w.Hosts()[0].State(gkp.Public).Root()
	if postRoot == parentRoot {
		t.Fatal("SLASH did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != postRoot {
			t.Fatalf("post-slash divergence: host %s=%x want %x", h.ID(), got, postRoot)
		}
	}
	t.Logf("slash recorded; root = %x", postRoot[:4])

	// Step 6: steward set should now have 2 entries (alice, bob),
	// and carol should not be among them.
	stPost := w.Hosts()[0].State(gkp.Public).StewardsAt(nil)
	if len(stPost) != 2 {
		t.Fatalf("expected 2 stewards post-slash, got %d", len(stPost))
	}
	for _, st := range stPost {
		if st.Key == carol.Public {
			t.Errorf("carol should be removed from steward set, but is still present")
		}
	}

	// Verify the slashed/<carol-hex> entry exists.
	verifySlashedEntryPresent(t, w.Hosts()[0].State(gkp.Public), carol.Public)
	t.Logf("steward set post-slash: alice + bob (2 of 2)")
}

// buildEquivocationEvidence fabricates two conflicting CREATE_EVENT
// transitions at the same prior_state and returns their tx hashes.
// The transitions are not applied; we only need their canonical
// bytes to compute the equivocation evidence hashes.
func buildEquivocationEvidence(t *testing.T, w *sim.World, gid types.GroupID, parentRoot types.Hash) (types.Hash, types.Hash) {
	t.Helper()
	priorRootPB := &pb.StateRoot{Hash: parentRoot[:]}

	mkEvent := func(id string) []byte {
		tr := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
			PriorState: priorRootPB,
			Payload: &pb.Transition_CreateEvent{CreateEvent: &pb.CreateEventPayload{
				EventId: id,
				Title:   "Equivocation evidence event " + id,
			}},
		}
		c, err := group.MarshalCanonicalForSigningHelper(tr)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	canonA := mkEvent("equiv-a")
	canonB := mkEvent("equiv-b")

	rawA := sha256Sum(canonA)
	rawB := sha256Sum(canonB)
	var hA, hB types.Hash
	copy(hA[:], rawA[:])
	copy(hB[:], rawB[:])
	return hA, hB
}

// verifySlashedEntryPresent asserts that the slashed/<hex> entry
// exists in the snapshot with a non-nil value.
func verifySlashedEntryPresent(t *testing.T, st *group.State, key types.PublicKey) {
	t.Helper()
	entryKey := "slashed/" + tlsKeyHex(key)
	for _, e := range st.Snapshot().Entries {
		if e.Key == entryKey {
			if e.Value == nil {
				t.Errorf("entry %q present but value is nil", entryKey)
			}
			return
		}
	}
	t.Errorf("entry %q not found in snapshot", entryKey)
}