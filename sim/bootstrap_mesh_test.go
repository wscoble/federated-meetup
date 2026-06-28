// SPDX-License-Identifier: MIT
//
// Bootstrap resolution scenario: CREATE_GROUP declares an initial
// mesh peer, which closes the chicken-and-egg gap where ADD_HOST_PEER
// requires an existing mesh member to co-sign but the mesh starts
// empty.
//
// What this exercises:
//   - CREATE_GROUP with initial_mesh_peers seeds the mesh registry
//     AND writes mesh_peer/<hex> entries to the Merkle KV
//   - Subsequent ADD_HOST_PEER succeeds when the cosigner is one
//     of the seeded initial peers (cosigner check now passes)
//   - Mesh grows: 0 -> 1 (bootstrap) -> 2 (ADD)
//   - REMOVE_HOST_PEER can then evict the added peer (target check
//     passes because peer is in registry)
//   - Cross-host convergence on every transition
//
// Why this matters: without CREATE_GROUP seeding, the mesh cannot
// be admitted to at all via the wire protocol. With seeding,
// founders declare their first peers at group creation, and the
// mesh can grow naturally from there. This is the design
// decision that emerged from Cycle 7 + 12's gap documentation.
package sim_test

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestBootstrap_InitialMeshPeers walks through:
//  1. CREATE_GROUP "bootstrap-test" with alice + bob as stewards,
//     threshold 2, AND initial_mesh_peers=[seedPeer1]
//  2. After CREATE_GROUP: MeshPeers() returns [seedPeer1] on all hosts
//  3. ADD_HOST_PEER with seedPeer1 as cosigner succeeds; mesh grows to 2
//  4. REMOVE_HOST_PEER for the newly-added peer succeeds (target is in registry)
//  5. Mesh returns to 1 (just the bootstrap seed)
//  6. All 4 hosts converge at each step
func TestBootstrap_InitialMeshPeers(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        70,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	mesh := sim.NewMesh(w, sim.DDILBenign)
	w.AttachMesh(mesh)

	// Steward signing keys.
	stewardSeeds := []uint64{w.DeriveSeed("alice"), w.DeriveSeed("bob")}
	stewards := make([]crypto.KeyPair, 2)
	for i, s := range stewardSeeds {
		var seed [32]byte
		for j := 0; j < 8; j++ {
			seed[j] = byte(s >> (8 * j))
		}
		stewards[i] = crypto.KeyPairFromSeed(seed)
	}

	// Group keypair.
	var groupSeed [32]byte
	gid := w.DeriveSeed("bootstrap-test-group")
	for j := 0; j < 8; j++ {
		groupSeed[j] = byte(gid >> (8 * j))
	}
	groupKP := crypto.KeyPairFromSeed(groupSeed)

	// Seed peer: wg key + Ed25519 priv for cosigning.
	var seedPeerSeed [32]byte
	sp := w.DeriveSeed("seed-peer-1")
	for j := 0; j < 8; j++ {
		seedPeerSeed[j] = byte(sp >> (8 * j))
	}
	seedPeerKP := crypto.KeyPairFromSeed(seedPeerSeed)
	seedPeerMeshIP := []byte{10, 0, 0, 1}

	// Step 1: CREATE_GROUP with initial_mesh_peers.
	createPayload := &pb.CreateGroupPayload{
		CanonicalName:   "bootstrap-test",
		DisplayName:     "Bootstrap Test Group",
		InitialStewards: stewardPBs(stewards),
		Threshold:       2,
		InitialMeshPeers: []*pb.InitialMeshPeer{
			{HostWgKey: seedPeerKP.Public[:], MeshIp: seedPeerMeshIP},
		},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(&pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		PriorState: nil,
		Payload:    &pb.Transition_CreateGroup{CreateGroup: createPayload},
		SignedAt:   timestamppb.New(w.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	multisig := &pb.Multisig{
		Threshold:  2,
		Signatures: sigsFor(stewards, groupKP.Public, canonical),
	}
	createTr, err := group.NewTransition(&pb.Transition{
		Type:              pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		PriorState:        nil,
		Payload:           &pb.Transition_CreateGroup{CreateGroup: createPayload},
		SignedAt:          timestamppb.New(w.Now()),
		StewardSignatures: multisig,
	}, groupKP.Public)
	if err != nil {
		t.Fatal(err)
	}

	for _, h := range w.Hosts() {
		h.AddGroup(groupKP.Public, createTr)
		if _, err := h.SubmitTransition(groupKP.Public, createTr); err != nil {
			t.Fatalf("CREATE_GROUP on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)

	// Step 2: verify mesh has the seed peer on all hosts.
	parentRoot := w.Hosts()[0].State(groupKP.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(groupKP.Public).Root(); got != parentRoot {
			t.Fatalf("post-CREATE divergence: host %s=%x want %x", h.ID(), got, parentRoot)
		}
		if got := h.State(groupKP.Public).MeshPeers(); len(got) != 1 {
			t.Fatalf("host %s has %d mesh peers post-CREATE, want 1", h.ID(), len(got))
		}
	}
	if peers := w.Hosts()[0].State(groupKP.Public).MeshPeers(); len(peers) != 1 {
		t.Fatalf("seed peer missing post-CREATE; got %d peers", len(peers))
	} else {
		t.Logf("CREATE_GROUP seeded 1 mesh peer: %x...", peers[0].HostWGKey.Raw[:4])
	}

	// Step 3: ADD_HOST_PEER for a new peer, using seedPeer1 as cosigner.
	var newPeerSeed [32]byte
	np := w.DeriveSeed("new-peer")
	for j := 0; j < 8; j++ {
		newPeerSeed[j] = byte(np >> (8 * j))
	}
	newPeerKP := crypto.KeyPairFromSeed(newPeerSeed)
	newPeerMeshIP := []byte{10, 0, 0, 2}

	addPayload := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: newPeerKP.Public[:]},
		MeshIp:          newPeerMeshIP,
		CosignerPeerKey: &pb.PublicKey{Raw: seedPeerKP.Public[:]},
	}
	// Compute the deterministic canonical bytes for the cosigner
	// signature (matching verifyCoSignerSignature in gates.go).
	cosigCanonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(addPayload)
	if err != nil {
		t.Fatal(err)
	}
	addPayload.CosignerPeerSignature = &pb.Signature{
		Raw: signCosigner(seedPeerSeed, cosigCanonical),
	}

	// Sign the full transition with the steward multisig.
	addTr, err := buildSignedTransitionForAddPeer(
		t, groupKP.Public, parentRoot, addPayload, stewards, 2)
	if err != nil {
		t.Fatal(err)
	}

	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(groupKP.Public, addTr); err != nil {
			t.Fatalf("ADD_HOST_PEER on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterAdd := w.Hosts()[0].State(groupKP.Public).Root()
	if rootAfterAdd == parentRoot {
		t.Fatal("ADD_HOST_PEER did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(groupKP.Public).Root(); got != rootAfterAdd {
			t.Fatalf("post-ADD divergence: host %s=%x want %x", h.ID(), got, rootAfterAdd)
		}
		if got := h.State(groupKP.Public).MeshPeers(); len(got) != 2 {
			t.Errorf("host %s has %d mesh peers post-ADD, want 2", h.ID(), len(got))
		}
	}
	t.Logf("ADD_HOST_PEER succeeded: mesh has 2 peers")

	// Step 4: REMOVE_HOST_PEER for the newly-added peer. Now succeeds.
	removePayload := &pb.RemoveHostPeerPayload{
		HostWgKey: &pb.PublicKey{Raw: newPeerKP.Public[:]},
		MeshIp:    newPeerMeshIP,
	}
	removeTr, err := buildSignedTransitionForRemovePeer(
		t, groupKP.Public, rootAfterAdd, removePayload, stewards, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(groupKP.Public, removeTr); err != nil {
			t.Fatalf("REMOVE_HOST_PEER on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterRemove := w.Hosts()[0].State(groupKP.Public).Root()
	if rootAfterRemove == rootAfterAdd {
		t.Fatal("REMOVE_HOST_PEER did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(groupKP.Public).Root(); got != rootAfterRemove {
			t.Fatalf("post-REMOVE divergence: host %s=%x want %x", h.ID(), got, rootAfterRemove)
		}
		if got := h.State(groupKP.Public).MeshPeers(); len(got) != 1 {
			t.Errorf("host %s has %d mesh peers post-REMOVE, want 1", h.ID(), len(got))
		}
	}
	t.Logf("REMOVE_HOST_PEER succeeded: mesh back to 1 peer")

	finalPeers := w.Hosts()[0].State(groupKP.Public).MeshPeers()
	if len(finalPeers) != 1 {
		t.Fatalf("expected 1 peer post-REMOVE, got %d", len(finalPeers))
	}
	t.Logf("bootstrap round-trip complete: 0 -> 1 (CREATE) -> 2 (ADD) -> 1 (REMOVE)")
}

// buildSignedTransitionForAddPeer constructs a fully-signed
// ADD_HOST_PEER transition for the bootstrap test.
func buildSignedTransitionForAddPeer(
	t *testing.T,
	gid types.GroupID,
	prior types.Hash,
	payload *pb.AddHostPeerPayload,
	signers []crypto.KeyPair,
	threshold uint32,
) (*group.Transition, error) {
	t.Helper()
	tr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		PriorState: &pb.StateRoot{Hash: prior[:]},
		Payload:    &pb.Transition_AddHostPeer{AddHostPeer: payload},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(tr)
	if err != nil {
		return nil, err
	}
	tr.StewardSignatures = &pb.Multisig{
		Threshold:  threshold,
		Signatures: sigsFor(signers[:threshold], gid, canonical),
	}
	return group.NewTransition(tr, gid)
}

// buildSignedTransitionForRemovePeer constructs a fully-signed
// REMOVE_HOST_PEER transition for the bootstrap test.
func buildSignedTransitionForRemovePeer(
	t *testing.T,
	gid types.GroupID,
	prior types.Hash,
	payload *pb.RemoveHostPeerPayload,
	signers []crypto.KeyPair,
	threshold uint32,
) (*group.Transition, error) {
	t.Helper()
	tr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_REMOVE_HOST_PEER,
		PriorState: &pb.StateRoot{Hash: prior[:]},
		Payload:    &pb.Transition_RemoveHostPeer{RemoveHostPeer: payload},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(tr)
	if err != nil {
		return nil, err
	}
	tr.StewardSignatures = &pb.Multisig{
		Threshold:  threshold,
		Signatures: sigsFor(signers[:threshold], gid, canonical),
	}
	return group.NewTransition(tr, gid)
}

// signCosigner signs a cosigner message using the Ed25519 view of
// the seed (which is also the wg pubkey). Mirrors
// internal/group/gates.go signStewardEd25519.
func signCosigner(seed [32]byte, msg []byte) []byte {
	priv := ed25519.NewKeyFromSeed(seed[:])
	prefixed := bootstrapDomainPrefix("add_host_peer_cosig", msg)
	return ed25519.Sign(priv, prefixed)
}

// bootstrapDomainPrefix mirrors internal/group/gates.go domainPrefix.
func bootstrapDomainPrefix(domain string, msg []byte) []byte {
	h := sha256.New()
	h.Write([]byte(domain))
	var lenBuf [8]byte
	binary.BigEndian.PutUint64(lenBuf[:], uint64(len(msg)))
	h.Write(lenBuf[:])
	h.Write(msg)
	return h.Sum(nil)
}