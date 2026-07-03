// SPDX-License-Identifier: AGPL-3.0
//
// Attest scenario: a steward group issues an attestation from one
// identity to another, recording the attestation in the Merkle KV.
//
// What this exercises:
//   - TRANSITION_TYPE_ATTEST: stores attest/<to-hex> entries keyed by
//     the recipient (to) identity. Multiple attestations to the same
//     recipient all collide on the same key; the protocol stores the
//     LATEST payload (last-write-wins) — the prior payload is
//     overwritten.
//   - Cross-host convergence on the post-attest transition
//   - State root advances on the attestation
//
// Why this matters: attestations are how reputation travels between
// identities. The host cert scenario proved G1 (TLS surface);
// attest is the reputation-layer primitive that lets stewards vouch
// for users.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// TestAttest_StewardAttestation walks through:
//  1. Vegas Programmers exist
//  2. Stewards sign an ATTEST from alice -> bob with schema
//     "endorsement.organizer"
//  3. attest/<bob-hex> entry appears in the snapshot on all 4 hosts
//  4. State root advances
//  5. A second ATTEST (alice -> bob, different schema) overwrites
//     the first at the same key — attestStorageKey is to-identity
//     only, so the latest payload wins
func TestAttest_StewardAttestation(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        50,
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

	// Pre-state: parent group converged.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-attest: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}

	// From = alice (a steward), To = bob (another steward).
	// (Stewards can attest too; in practice attestations are about
	// organizer endorsements of regular users, but the test just
	// exercises the protocol path.)
	from := stewards[0]
	to := stewards[1]

	// Step 2: ATTEST alice -> bob, schema "endorsement.organizer".
	attestPayload := &pb.AttestPayload{
		FromIdentity: &pb.PublicKey{Raw: from.Public[:]},
		ToIdentity:   &pb.PublicKey{Raw: to.Public[:]},
		Schema:       "endorsement.organizer",
		Payload:      []byte("alice endorses bob as an organizer"),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ATTEST organizer",
		pb.TransitionType_TRANSITION_TYPE_ATTEST,
		attestPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}

	// Step 3: attest entry should be present on all hosts.
	attestKey := "attest/" + tlsKeyHex(to.Public)
	firstRoot := w.Hosts()[0].State(gkp.Public).Root()
	if firstRoot == parentRoot {
		t.Fatal("ATTEST did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != firstRoot {
			t.Fatalf("post-attest divergence: host %s=%x want %x", h.ID(), got, firstRoot)
		}
	}
	verifyAttestEntryPresent(t, w.Hosts()[0].State(gkp.Public), attestKey)
	t.Logf("first attest recorded; root = %x", firstRoot[:4])

	// Step 4: a second ATTEST (different schema) at the same key
	// should overwrite. attestStorageKey is to-identity only, so
	// both attestations collide on attest/<bob-hex>.
	secondAttest := &pb.AttestPayload{
		FromIdentity: &pb.PublicKey{Raw: from.Public[:]},
		ToIdentity:   &pb.PublicKey{Raw: to.Public[:]},
		Schema:       "endorsement.speaker",
		Payload:      []byte("alice endorses bob as a speaker"),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ATTEST speaker",
		pb.TransitionType_TRANSITION_TYPE_ATTEST,
		secondAttest,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}

	// Step 5: same key, but the root advanced (so the value is
	// different — latest payload wins).
	secondRoot := w.Hosts()[0].State(gkp.Public).Root()
	if secondRoot == firstRoot {
		t.Fatal("second ATTEST did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != secondRoot {
			t.Fatalf("post-second-attest divergence: host %s=%x want %x", h.ID(), got, secondRoot)
		}
	}
	verifyAttestEntryPresent(t, w.Hosts()[0].State(gkp.Public), attestKey)
	t.Logf("second attest recorded; root = %x", secondRoot[:4])
}

// verifyAttestEntryPresent asserts that an entry with the given key
// exists in the snapshot with a non-nil value. The value will be a
// marshaled AttestPayload — tests can decode it via proto.Unmarshal.
func verifyAttestEntryPresent(t *testing.T, st *group.State, key string) {
	t.Helper()
	for _, e := range st.Snapshot().Entries {
		if e.Key == key {
			if e.Value == nil {
				t.Errorf("entry %q present but value is nil", key)
			}
			return
		}
	}
	t.Errorf("entry %q not found in snapshot", key)
}