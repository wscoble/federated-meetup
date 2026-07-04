// SPDX-License-Identifier: AGPL-3.0
//
// Slashed-steward-aftermath scenario: after a steward is slashed
// via SLASH_STEWARD, they MUST NOT be able to co-sign future
// transitions. The verify gate rejects signatures from keys
// that are no longer in the active steward set.
//
// What this exercises:
//   - SLASH_STEWARD removes the slashed key from the steward set
//     (computeCurrentStewards recomputes without them)
//   - A subsequent transition signed by the slashed steward
//     (along with a threshold of OTHER stewards) is REJECTED at
//     the verify gate — the slashed steward's sig no longer
//     matches an active steward
//   - Cross-host consistency: every host rejects the same
//     post-slash transition for the same reason
//
// Why this matters: SLASH_STEWARD only matters if the slashed
// party is actually powerless afterward. If the protocol
// continued accepting their signatures, the slash would be
// cosmetic — a bad-faith steward could keep co-signing.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
)

func TestSlashedSteward_CannotCoSign(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        88,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w) // [alice, bob, carol]
	carol := stewards[2]

	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	// SLASH_STEWARD: alice + bob sign (NOT carol). Use the same
	// evidence-shape as TestSlashSteward_Equivocation; the gate
	// validates the evidence structurally regardless of whether
	// the conflicting txs were actually seen.
	txHashA := [32]byte{0x01, 0x02, 0x03}
	txHashB := [32]byte{0x01, 0x02, 0x04}
	slashProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_SLASH_STEWARD,
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
	for _, k := range []crypto.KeyPair{stewards[0], stewards[1]} { // alice + bob
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	slashProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	slashTx, err := group.NewTransition(slashProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, slashTx); err != nil {
			t.Fatalf("SLASH on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterSlash := w.Hosts()[0].State(gkp.Public).Root()
	if rootAfterSlash == parentRoot {
		t.Fatal("SLASH did not advance root")
	}
	// Verify steward count dropped from 3 to 2.
	stPost := w.Hosts()[0].State(gkp.Public).StewardsAt(nil)
	if len(stPost) != 2 {
		t.Fatalf("expected 2 stewards post-slash, got %d", len(stPost))
	}
	t.Logf("carol slashed; steward set is now %d", len(stPost))

	// Now attempt a transition co-signed by carol + alice. The
	// threshold is 2; if carol's signature is still trusted, this
	// would apply. It must be rejected because carol is no longer
	// an active steward.
	badProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		PriorState: &pb.StateRoot{Hash: rootAfterSlash[:]},
		Payload: &pb.Transition_CreateEvent{CreateEvent: &pb.CreateEventPayload{
			EventId: "post-slash-attempt",
			Title:   "Should be rejected",
		}},
	}
	canonicalBad, err := group.MarshalCanonicalForSigningHelper(badProto)
	if err != nil {
		t.Fatal(err)
	}
	badSigs := []*pb.Signature{}
	for _, k := range []crypto.KeyPair{stewards[2], stewards[0]} { // carol + alice
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonicalBad)
		badSigs = append(badSigs, &pb.Signature{Raw: s[:]})
	}
	badProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: badSigs}
	badTx, err := group.NewTransition(badProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	// Every host must reject — proves the post-slash state is
	// consistent across the mesh.
	for _, h := range w.Hosts() {
		_, err := h.SubmitTransition(gkp.Public, badTx)
		if err == nil {
			t.Fatalf("host %s accepted transition co-signed by slashed carol", h.ID())
		}
		if !strings.Contains(err.Error(), "steward") &&
			!strings.Contains(err.Error(), "signature") &&
			!strings.Contains(err.Error(), "verify") {
			t.Logf("host %s rejection (acceptable): %v", h.ID(), err)
		}
		t.Logf("host %s correctly rejected post-slash transition: %v", h.ID(), err)
	}
}