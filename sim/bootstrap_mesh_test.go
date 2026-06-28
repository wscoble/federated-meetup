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

// TestBootstrap_RemoveThenReAddPeer verifies the post-REMOVE
// state root is distinct from the post-CREATE root when the
// only ADD'd peer is removed. If REMOVE_HOST_PEER collapses
// the Merkle KV such that the post-REMOVE root equals the
// post-CREATE root, a subsequent ADD_HOST_PEER for the same
// peer would collide with the CREATE_GROUP signatures recorded
// in the equivocation log and trigger a spurious rejection.
//
// This is the same root-collapse class of bug fixed in cycle 25
// (REMOVE_MEMBER) and cycle 26 (CANCEL_RSVP). See
// internal/group/group.go REMOVE_HOST_PEER case.
//
// What this exercises:
//   - State root advances through ADD → REMOVE → ADD for the
//     same host peer.
//   - All 4 hosts converge at each step.
//   - Final mesh size = 2 (1 bootstrap + 1 re-added).
func TestBootstrap_RemoveThenReAddPeer(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        71,
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
	gid := w.DeriveSeed("bootstrap-readd-group")
	for j := 0; j < 8; j++ {
		groupSeed[j] = byte(gid >> (8 * j))
	}
	groupKP := crypto.KeyPairFromSeed(groupSeed)

	// Seed peer (cosigner for first ADD).
	var seedPeerSeed [32]byte
	sp := w.DeriveSeed("seed-peer-readd")
	for j := 0; j < 8; j++ {
		seedPeerSeed[j] = byte(sp >> (8 * j))
	}
	seedPeerKP := crypto.KeyPairFromSeed(seedPeerSeed)
	seedPeerMeshIP := []byte{10, 0, 0, 1}

	// Peer that will be ADD'd, REMOVE'd, and ADD'd again.
	var targetPeerSeed [32]byte
	tp := w.DeriveSeed("target-peer")
	for j := 0; j < 8; j++ {
		targetPeerSeed[j] = byte(tp >> (8 * j))
	}
	targetPeerKP := crypto.KeyPairFromSeed(targetPeerSeed)
	targetPeerMeshIP := []byte{10, 0, 0, 2}

	// CREATE_GROUP with initial_mesh_peers=[seedPeer].
	createPayload := &pb.CreateGroupPayload{
		CanonicalName:   "bootstrap-readd",
		DisplayName:     "Bootstrap Read-add Group",
		InitialStewards: stewardPBs(stewards),
		Threshold:       2,
		InitialMeshPeers: []*pb.InitialMeshPeer{
			{HostWgKey: seedPeerKP.Public[:], MeshIp: seedPeerMeshIP},
		},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(&pb.Transition{
		Type:     pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload:  &pb.Transition_CreateGroup{CreateGroup: createPayload},
		SignedAt: timestamppb.New(w.Now()),
	})
	if err != nil {
		t.Fatal(err)
	}
	createTr := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload:    &pb.Transition_CreateGroup{CreateGroup: createPayload},
		SignedAt:   timestamppb.New(w.Now()),
		StewardSignatures: &pb.Multisig{
			Threshold:  2,
			Signatures: sigsFor(stewards, groupKP.Public, canonical),
		},
	}
	tx, err := group.NewTransition(createTr, groupKP.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		h.AddGroup(groupKP.Public, tx)
		if _, err := h.SubmitTransition(groupKP.Public, tx); err != nil {
			t.Fatalf("CREATE_GROUP on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	parentRoot := w.Hosts()[0].State(groupKP.Public).Root()
	t.Logf("CREATE applied; parentRoot = %x", parentRoot[:4])

	// ADD_HOST_PEER target with seedPeer as cosigner.
	addPayload := &pb.AddHostPeerPayload{
		HostWgKey:        &pb.PublicKey{Raw: targetPeerKP.Public[:]},
		MeshIp:           targetPeerMeshIP,
		CosignerPeerKey:  &pb.PublicKey{Raw: seedPeerKP.Public[:]},
	}
	cosigCanonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(addPayload)
	if err != nil {
		t.Fatal(err)
	}
	addPayload.CosignerPeerSignature = &pb.Signature{
		Raw: signCosigner(seedPeerSeed, cosigCanonical),
	}
	addTr, err := buildSignedTransitionForAddPeer(
		t, groupKP.Public, parentRoot, addPayload, stewards, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(groupKP.Public, addTr); err != nil {
			t.Fatalf("ADD on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterAdd := w.Hosts()[0].State(groupKP.Public).Root()
	if rootAfterAdd == parentRoot {
		t.Fatal("ADD did not advance root")
	}
	t.Logf("ADD applied; rootAfterAdd = %x", rootAfterAdd[:4])

	// REMOVE_HOST_PEER target.
	removePayload := &pb.RemoveHostPeerPayload{
		HostWgKey: &pb.PublicKey{Raw: targetPeerKP.Public[:]},
		MeshIp:    targetPeerMeshIP,
	}
	removeTr, err := buildSignedTransitionForRemovePeer(
		t, groupKP.Public, rootAfterAdd, removePayload, stewards, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(groupKP.Public, removeTr); err != nil {
			t.Fatalf("REMOVE on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterRemove := w.Hosts()[0].State(groupKP.Public).Root()
	if rootAfterRemove == rootAfterAdd {
		t.Fatal("REMOVE did not advance root")
	}
	if rootAfterRemove == parentRoot {
		t.Fatalf("REMOVE collapsed root to post-CREATE root (%x); tombstone required to avoid spurious equivocation on re-ADD", parentRoot[:4])
	}
	t.Logf("REMOVE applied; rootAfterRemove = %x (distinct from parent %x)", rootAfterRemove[:4], parentRoot[:4])

	// ADD_HOST_PEER target AGAIN. Must NOT spuriously trigger
	// equivocation. This is the regression check.
	add2Payload := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: targetPeerKP.Public[:]},
		MeshIp:          targetPeerMeshIP,
		CosignerPeerKey: &pb.PublicKey{Raw: seedPeerKP.Public[:]},
	}
	cosigCanonical2, err := proto.MarshalOptions{Deterministic: true}.Marshal(add2Payload)
	if err != nil {
		t.Fatal(err)
	}
	add2Payload.CosignerPeerSignature = &pb.Signature{
		Raw: signCosigner(seedPeerSeed, cosigCanonical2),
	}
	add2Tr, err := buildSignedTransitionForAddPeer(
		t, groupKP.Public, rootAfterRemove, add2Payload, stewards, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(groupKP.Public, add2Tr); err != nil {
			t.Fatalf("re-ADD on host %s: %v (regression: REMOVE_HOST_PEER likely collapsed root)", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterReAdd := w.Hosts()[0].State(groupKP.Public).Root()
	if rootAfterReAdd == rootAfterRemove {
		t.Fatal("re-ADD did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(groupKP.Public).Root(); got != rootAfterReAdd {
			t.Fatalf("post-reADD divergence: host %s=%x want %x", h.ID(), got, rootAfterReAdd)
		}
		if got := h.State(groupKP.Public).MeshPeers(); len(got) != 2 {
			t.Errorf("host %s has %d mesh peers post-reADD, want 2", h.ID(), len(got))
		}
	}
	t.Logf("re-ADD applied; rootAfterReAdd = %x; mesh has 2 peers", rootAfterReAdd[:4])
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