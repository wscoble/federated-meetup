// SPDX-License-Identifier: AGPL-3.0
//
// Remove-mesh-peer scenario: REMOVE_HOST_PEER depends on the
// target peer being a current mesh member. Combined with the
// ADD_HOST_PEER bootstrap gap (Cycle 7), this means the mesh
// cannot grow OR shrink via the protocol alone without a
// bootstrap primitive.
//
// What this exercises:
//   - REMOVE_HOST_PEER is rejected when the target peer is not
//     in the mesh registry (ErrUnknownMeshPeer)
//   - The state root does NOT advance on the rejection
//   - Cross-host divergence: no host sees the transition apply
//
// Why this matters: documents the symmetric dependency to the
// ADD_HOST_PEER gap. Together they imply CREATE_GROUP needs a
// way to seed initial mesh members, OR there needs to be a
// separate genesis bootstrap transition. Until that decision
// lands, mesh peer membership is provably a no-op via the wire
// protocol.
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

// TestRemoveHostPeer_RequiresExistingPeer walks through:
//  1. Vegas Programmers exist (no mesh members yet)
//  2. Stewards sign REMOVE_HOST_PEER for some wg key
//  3. Apply is rejected: ErrUnknownMeshPeer (the target was
//     never added)
//  4. State root does NOT advance
//  5. MeshPeers() returns an empty list on all hosts
func TestRemoveHostPeer_RequiresExistingPeer(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        54,
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

	// Pre-state: parent converged, no mesh members.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-remove: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}
	for _, h := range w.Hosts() {
		if got := h.State(gkp.Public).MeshPeers(); len(got) != 0 {
			t.Fatalf("host %s has %d mesh peers pre-remove, want 0", h.ID(), len(got))
		}
	}

	// Build a REMOVE_HOST_PEER payload targeting a non-existent peer.
	targetKP := keyPairFromSeed(w, "non-existent-peer")
	removePayload := &pb.RemoveHostPeerPayload{
		HostWgKey: &pb.PublicKey{Raw: targetKP.Public[:]},
		MeshIp:    []byte{10, 0, 0, 99},
	}
	trProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_REMOVE_HOST_PEER,
		PriorState: &pb.StateRoot{Hash: parentRoot[:]},
		Payload:    &pb.Transition_RemoveHostPeer{RemoveHostPeer: removePayload},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
	if err != nil {
		t.Fatal(err)
	}
	sigs := make([]*pb.Signature, 0, 2)
	for _, k := range []crypto.KeyPair{stewards[0], stewards[1]} {
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	trProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: sigs}
	tx, err := group.NewTransition(trProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	// Submit. Should fail with unknown_mesh_peer.
	h0 := w.Hosts()[0]
	if _, err := h0.SubmitTransition(gkp.Public, tx); err == nil {
		t.Fatal("REMOVE_HOST_PEER for unknown peer should have been rejected")
	} else if !strings.Contains(err.Error(), "unknown_mesh_peer") {
		t.Fatalf("expected unknown_mesh_peer error, got: %v", err)
	} else {
		t.Logf("REMOVE_HOST_PEER correctly rejected: %v", err)
	}

	// Verify state did NOT advance on any host.
	for _, h := range w.Hosts() {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Errorf("host %s root advanced despite rejection: %x want %x",
				h.ID(), got, parentRoot)
		}
		if got := h.State(gkp.Public).MeshPeers(); len(got) != 0 {
			t.Errorf("host %s mesh peers non-empty after rejection: %d", h.ID(), len(got))
		}
	}

	// Confirm the gap is symmetric: ADD and REMOVE both fail
	// without bootstrap, just for different reasons. ADD fails on
	// the cosigner check; REMOVE fails on the unknown peer check.
	// Neither can grow OR shrink the mesh on its own.
	t.Log("symmetric gap: ADD_HOST_PEER fails on cosigner check; REMOVE_HOST_PEER fails on unknown_mesh_peer")
	_ = types.PublicKey{} // keep import alive for future expansion
}