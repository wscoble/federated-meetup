// SPDX-License-Identifier: MIT
//
// ADD_HOST_PEER scenario: documenting the bootstrap gap.
//
// What this should test:
//   - ADD_HOST_PEER admits a new wg peer to the mesh, requiring
//     steward threshold signature AND co-signature from an existing
//     mesh member
//   - The mesh registry reflects the new peer on all hosts
//   - Cross-host convergence on the post-add transition
//
// What this test actually surfaces:
//   - The protocol has no bootstrap path. The first ADD_HOST_PEER
//     requires a co-signer who's already a mesh member, but no
//     member can exist without already being admitted. This is a
//     chicken-and-egg problem — the first peer has to come from
//     somewhere (CREATE_GROUP seeding? host setup? out-of-band?).
//
// This test asserts the EXPECTED behavior (first ADD_HOST_PEER is
// rejected because no mesh member exists to co-sign). When the
// bootstrap path is implemented (likely via CREATE_GROUP seeding
// initial mesh members, or via a separate genesis transition),
// this test should be extended to add the bootstrap step first
// and then verify the post-bootstrap ADD_HOST_PEER succeeds.
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

// TestAddHostPeer_BootstrapGap walks through:
//  1. Vegas Programmers exist (no mesh members yet)
//  2. Stewards attempt to ADD_HOST_PEER for a new wg peer with
//     cosigner_peer_key set to a random key
//  3. Apply is rejected with a "cosigner not a mesh member" error
//  4. State root does NOT advance — the failed transition is
//     recorded as evidence but does not mutate state
func TestAddHostPeer_BootstrapGap(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        49,
		HostCount:   1, // single host — we're testing the state machine's gate
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	h0 := w.Hosts()[0]
	st := h0.State(gkp.Public)

	// Pre-state: 1 transition (CREATE_GROUP), no mesh members.
	if got := len(st.Log()); got != 1 {
		t.Fatalf("expected log size 1, got %d", got)
	}

	// Build an ADD_HOST_PEER payload. The cosigner's wg key is
	// randomly chosen; it is NOT a current mesh member.
	cosignerKP := keyPairFromSeed(w, "fake-cosigner")
	newPeerKP := keyPairFromSeed(w, "new-peer")
	meshIP := []byte{10, 0, 0, 5}

	// Compute the cosigner signature FIRST (before wrapping into
	// the AddHostPeerPayload). The verifier treats the cosigner's
	// wg key as an Ed25519 key and verifies the signature over
	// the canonical AddHostPeerPayload bytes. We need to compute
	// the signature before constructing the full payload (which
	// would include the signature itself, causing a chicken-and-egg).
	cosignerSig := buildCosignerSignatureForKey(t, cosignerKP)

	addPeerPayload := &pb.AddHostPeerPayload{
		HostWgKey:              &pb.PublicKey{Raw: newPeerKP.Public[:]},
		MeshIp:                 meshIP,
		CosignerPeerKey:        &pb.PublicKey{Raw: cosignerKP.Public[:]},
		CosignerPeerSignature:  &pb.Signature{Raw: cosignerSig},
	}

	// Build the full transition. The steward envelope requires 2
	// sigs (threshold 2 of 3 stewards).
	trProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		PriorState: nil,
		Payload:    &pb.Transition_AddHostPeer{AddHostPeer: addPeerPayload},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
	if err != nil {
		t.Fatal(err)
	}
	sigs := make([]*pb.Signature, 2)
	for i, k := range stewards[:2] {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs[i] = &pb.Signature{Raw: s[:]}
	}
	trProto.StewardSignatures = &pb.Multisig{
		Threshold:  2,
		Signatures: sigs,
	}

	tx, err := group.NewTransition(trProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	// Submit. Should be rejected because cosigner is not a mesh member.
	_, err = h0.SubmitTransition(gkp.Public, tx)
	if err == nil {
		t.Fatal("ADD_HOST_PEER should have been rejected (no mesh members to co-sign), but Apply succeeded")
	}
	t.Logf("ADD_HOST_PEER correctly rejected: %v", err)

	// Verify the rejection mentions mesh membership (the gate we're testing).
	if !strings.Contains(err.Error(), "mesh member") {
		t.Errorf("rejection should mention mesh membership, got: %v", err)
	}

	// Verify state was NOT mutated (log size still 1).
	if got := len(st.Log()); got != 1 {
		t.Errorf("log size = %d, want 1 (rejected transition should not advance state)", got)
	}
}

// buildCosignerSignatureForKey returns a placeholder Ed25519
// signature from the given keypair. In real life the signature
// would be over the canonical AddHostPeerPayload bytes; for the
// bootstrap-gap test we only need to get past the prior_state
// check and reach the IsMeshMemberLocked gate. The co-signature
// verify happens AFTER the mesh-membership check (see gates.go
// verifyAddHostPeerPayload), so a placeholder is fine — the test
// will be rejected on mesh membership before signature verify.
//
// We still need a structurally valid Ed25519 signature (64 bytes)
// to avoid a parse error in the verifier.
func buildCosignerSignatureForKey(t *testing.T, kp crypto.KeyPair) []byte {
	t.Helper()
	// Sign a dummy message; the verifier rejects on mesh membership
	// before checking the signature, so the content doesn't matter.
	var dummy [32]byte
	sig := crypto.Sign(kp, types.PublicKey{}, crypto.MsgKindTransition, dummy[:])
	return sig[:]
}