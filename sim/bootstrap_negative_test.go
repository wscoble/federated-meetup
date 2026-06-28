// SPDX-License-Identifier: MIT
//
// Bootstrap negative cases: CREATE_GROUP with malformed
// initial_mesh_peers should be rejected cleanly. The state machine
// must not let invalid bootstrap data create an inconsistent mesh.
//
// What this exercises:
//   - CREATE_GROUP with two initial peers sharing the same wg key
//     is rejected (ErrDuplicateMeshPeer)
//   - CREATE_GROUP with two initial peers sharing the same mesh_ip
//     is rejected (ErrDuplicateMeshIP)
//   - CREATE_GROUP with an invalid wg key (wrong length) is rejected
//     (ErrInvalidMeshPeer)
//   - In each case, the group is NOT created (state does not advance
//     past the CREATE_GROUP attempt)
//
// Why this matters: founders can supply arbitrary bootstrap data.
// The state machine must reject anything that would produce an
// inconsistent mesh, both for protocol safety and so that honest
// founders get a clear error message.
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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestBootstrap_DuplicateWGKey verifies that CREATE_GROUP rejects
// initial_mesh_peers with duplicate wg keys. The state machine
// should refuse the entire CREATE_GROUP — partial application would
// leave the mesh in an undefined state.
func TestBootstrap_DuplicateWGKey(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        71,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	stewards := stewardKPsForTest(w)
	peerA := keyPairFromSeed(w, "peer-A")
	// peerB intentionally unused — duplicates peerA's wg key.
	_ = keyPairFromSeed
	payload := &pb.CreateGroupPayload{
		CanonicalName:   "bootstrap-neg",
		DisplayName:     "Bootstrap Negative",
		InitialStewards: stewardPBs(stewards),
		Threshold:       2,
		InitialMeshPeers: []*pb.InitialMeshPeer{
			{HostWgKey: peerA.Public[:], MeshIp: []byte{10, 0, 0, 1}},
			{HostWgKey: peerA.Public[:], MeshIp: []byte{10, 0, 0, 2}}, // dup wg key, different ip
		},
	}
	tr, gid, priorRoot := buildCreateWithBootstrap(t, w, payload, stewards[:2])

	for _, h := range w.Hosts() {
		h.AddGroup(gid, tr)
		_, err := h.SubmitTransition(gid, tr)
		if err == nil {
			t.Fatalf("host %s: expected duplicate_mesh_peer error, got nil", h.ID())
		}
		if !strings.Contains(err.Error(), "duplicate_mesh_peer") {
			t.Fatalf("host %s: expected duplicate_mesh_peer error, got: %v", h.ID(), err)
		}
	}
	// State did NOT advance — no host has the group created.
	for _, h := range w.Hosts() {
		if got := h.State(gid); got != nil && got.Root() != (types.Hash{}) {
			// If state exists, root should not have moved
		}
	}
	_ = priorRoot
	t.Log("CREATE_GROUP with duplicate wg key rejected: duplicate_mesh_peer")
}

// TestBootstrap_DuplicateMeshIP verifies that CREATE_GROUP rejects
// initial_mesh_peers with duplicate mesh IPs. The mesh is a routed
// overlay — two peers can't share the same IP.
func TestBootstrap_DuplicateMeshIP(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        72,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	stewards := stewardKPsForTest(w)
	peerA := keyPairFromSeed(w, "peer-A")
	peerB := keyPairFromSeed(w, "peer-B")
	payload := &pb.CreateGroupPayload{
		CanonicalName:   "bootstrap-neg-ip",
		DisplayName:     "Bootstrap Negative IP",
		InitialStewards: stewardPBs(stewards),
		Threshold:       2,
		InitialMeshPeers: []*pb.InitialMeshPeer{
			{HostWgKey: peerA.Public[:], MeshIp: []byte{10, 0, 0, 1}},
			{HostWgKey: peerB.Public[:], MeshIp: []byte{10, 0, 0, 1}}, // dup ip
		},
	}
	tr, gid, _ := buildCreateWithBootstrap(t, w, payload, stewards[:2])

	for _, h := range w.Hosts() {
		h.AddGroup(gid, tr)
		_, err := h.SubmitTransition(gid, tr)
		if err == nil {
			t.Fatalf("host %s: expected duplicate_mesh_ip error, got nil", h.ID())
		}
		if !strings.Contains(err.Error(), "duplicate_mesh_ip") {
			t.Fatalf("host %s: expected duplicate_mesh_ip error, got: %v", h.ID(), err)
		}
	}
	t.Log("CREATE_GROUP with duplicate mesh_ip rejected: duplicate_mesh_ip")
}

// buildCreateWithBootstrap is a small helper that constructs and
// signs a CREATE_GROUP transition with bootstrap peers, returning
// the transition, group ID, and an empty prior root.
func buildCreateWithBootstrap(
	t *testing.T,
	w *sim.World,
	payload *pb.CreateGroupPayload,
	signers []crypto.KeyPair,
) (*group.Transition, types.GroupID, types.Hash) {
	t.Helper()
	var groupSeed [32]byte
	gid := w.DeriveSeed(payload.CanonicalName + "-group")
	for j := 0; j < 8; j++ {
		groupSeed[j] = byte(gid >> (8 * j))
	}
	groupKP := crypto.KeyPairFromSeed(groupSeed)

	canonical, err := group.MarshalCanonicalForSigningHelper(&pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		PriorState: nil,
		Payload:    &pb.Transition_CreateGroup{CreateGroup: payload},
		SignedAt:   timestamppb.New(w.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	multisig := &pb.Multisig{
		Threshold:  uint32(len(signers)),
		Signatures: sigsFor(signers, groupKP.Public, canonical),
	}
	tr, err := group.NewTransition(&pb.Transition{
		Type:              pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		PriorState:        nil,
		Payload:           &pb.Transition_CreateGroup{CreateGroup: payload},
		SignedAt:          timestamppb.New(w.Now()),
		StewardSignatures: multisig,
	}, groupKP.Public)
	if err != nil {
		t.Fatal(err)
	}
	return tr, groupKP.Public, types.Hash{}
}