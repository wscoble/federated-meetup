// SPDX-License-Identifier: AGPL-3.0
//
// Custody scenario: stewards declare the operational reality of
// how their keys are stored (HSM, hardware wallet, encrypted disk,
// live system, scripted). The declaration enables threshold
// policies like "M of N signers must be HSM-or-better".
//
// What this exercises:
//   - DECLARE_STEWARD_CUSTODY: stores custody/<hex> entry with the
//     declared tier byte. Latest declaration wins (last-write-wins
//     on the per-steward key).
//   - The verify gate rejects declarations from non-stewards
//   - Cross-host convergence on the post-declaration transition
//
// Why this matters: without custody declarations, threshold policy
// can't distinguish between "5-of-7 hardware-wallet signers" and
// "5-of-7 hot-server signers". Both produce a valid multisig from
// the protocol's perspective, but the operational security is very
// different. Custody declarations let the protocol reason about
// that.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// TestDeclareStewardCustody walks through:
//  1. Vegas Programmers exist (alice, bob, carol)
//  2. Alice declares HSM custody tier
//  3. custody/<alice-hex> entry appears with tier byte 1
//  4. Bob declares HARDWARE_WALLET custody (tier 2)
//  5. Bob's custody entry is at custody/<bob-hex>, value byte 2
//  6. All 4 hosts converge
//  7. A non-steward (eve) attempts to declare — verify gate rejects
func TestDeclareStewardCustody(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        52,
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
	eve := keyPairFromSeed(w, "eve-not-a-steward")

	// Pre-state: parent converged.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-custody: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}

	// Step 2: alice declares HSM.
	aliceCustody := &pb.DeclareStewardCustodyPayload{
		Steward:      &pb.PublicKey{Raw: stewards[0].Public[:]},
		Tier:         pb.CustodyTier_CUSTODY_TIER_HSM,
		Justification: "FIPS-validated HSM at office; air-gapped signing",
	}
	if !applyBroadcastFor(t, w, gkp.Public, "alice CUSTODY HSM",
		pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY,
		aliceCustody,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	aliceRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != aliceRoot {
			t.Fatalf("post-alice-custody divergence: host %s=%x want %x", h.ID(), got, aliceRoot)
		}
	}
	verifyCustodyEntry(t, w.Hosts()[0].State(gkp.Public), stewards[0].Public, 1)
	t.Logf("alice HSM custody recorded; root = %x", aliceRoot[:4])

	// Step 3: bob declares HARDWARE_WALLET.
	bobCustody := &pb.DeclareStewardCustodyPayload{
		Steward:      &pb.PublicKey{Raw: stewards[1].Public[:]},
		Tier:         pb.CustodyTier_CUSTODY_TIER_HARDWARE_WALLET,
		Justification: "Ledger Nano X in home safe",
	}
	if !applyBroadcastFor(t, w, gkp.Public, "bob CUSTODY HARDWARE_WALLET",
		pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY,
		bobCustody,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	bobRoot := w.Hosts()[0].State(gkp.Public).Root()
	if bobRoot == aliceRoot {
		t.Fatal("bob custody did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != bobRoot {
			t.Fatalf("post-bob-custody divergence: host %s=%x want %x", h.ID(), got, bobRoot)
		}
	}
	verifyCustodyEntry(t, w.Hosts()[0].State(gkp.Public), stewards[1].Public, 2)
	t.Logf("bob HARDWARE_WALLET custody recorded; root = %x", bobRoot[:4])

	// Step 4: eve (not a steward) attempts to declare custody. The
	// verify gate checks that the steward key is in the current
	// steward set — eve isn't, so the transition should be rejected.
	eveCustody := &pb.DeclareStewardCustodyPayload{
		Steward: &pb.PublicKey{Raw: eve.Public[:]},
		Tier:    pb.CustodyTier_CUSTODY_TIER_LIVE_SYSTEM,
	}
	trProto := buildCustodyTransition(t, w, gkp.Public, bobRoot, eveCustody,
		[]crypto.KeyPair{stewards[0], stewards[1]})
	h0 := w.Hosts()[0]
	if _, err := h0.SubmitTransition(gkp.Public, trProto); err == nil {
		t.Fatal("eve (non-steward) custody declaration should have been rejected")
	} else if !strings.Contains(err.Error(), "not a current steward") {
		t.Fatalf("expected 'not a current steward' error, got: %v", err)
	} else {
		t.Logf("eve custody correctly rejected: %v", err)
	}

	// State must NOT have advanced past bobRoot.
	for _, h := range w.Hosts() {
		if got := h.State(gkp.Public).Root(); got != bobRoot {
			t.Fatalf("eve custody should not have advanced root on host %s: %x want %x",
				h.ID(), got, bobRoot)
		}
	}
}

// buildCustodyTransition is a small helper for the negative test —
// lets the test build a transition with an explicit prior_state
// rather than going through applyBroadcastFor (which advances the
// world clock and uses the current head as prior).
func buildCustodyTransition(
	t *testing.T,
	w *sim.World,
	gid types.GroupID,
	priorRoot types.Hash,
	payload *pb.DeclareStewardCustodyPayload,
	signWith []crypto.KeyPair,
) *group.Transition {
	t.Helper()
	trProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY,
		PriorState: &pb.StateRoot{Hash: priorRoot[:]},
	}
	if err := setTransitionPayload(trProto, payload); err != nil {
		t.Fatal(err)
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
	if err != nil {
		t.Fatal(err)
	}
	sigs := make([]*pb.Signature, 0, len(signWith))
	for _, k := range signWith {
		s := crypto.Sign(k, gid, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	trProto.StewardSignatures = &pb.Multisig{
		Threshold:  uint32(len(signWith)),
		Signatures: sigs,
	}
	tr, err := group.NewTransition(trProto, gid)
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

// verifyCustodyEntry asserts that the custody/<hex> entry exists
// with the expected tier byte value.
func verifyCustodyEntry(t *testing.T, st *group.State, key types.PublicKey, expectedTier byte) {
	t.Helper()
	entryKey := "custody/" + tlsKeyHex(key)
	for _, e := range st.Snapshot().Entries {
		if e.Key == entryKey {
			if len(e.Value) != 1 {
				t.Errorf("custody entry %q has %d bytes, want 1", entryKey, len(e.Value))
				return
			}
			if e.Value[0] != expectedTier {
				t.Errorf("custody entry %q has tier %d, want %d", entryKey, e.Value[0], expectedTier)
			}
			return
		}
	}
	t.Errorf("custody entry %q not found in snapshot", entryKey)
}