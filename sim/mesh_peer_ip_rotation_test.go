// SPDX-License-Identifier: AGPL-3.0
//
// Mesh peer IP rotation. The meshPeerRegistry.add (internal/group/mesh.go:73)
// rejects a new peer if either:
//   - the wg key already exists in the registry (ErrDuplicateMeshPeer)
//   - the mesh IP is already assigned to another peer (ErrDuplicateMeshIP)
//
// This means a peer's mesh IP CANNOT be updated via ADD_HOST_PEER alone.
// The only documented workflow is REMOVE_HOST_PEER + ADD_HOST_PEER with
// the new IP.
//
// This test pins down all three boundaries:
//
//   1. ADD with same wg key (different IP) → ErrDuplicateMeshPeer
//   2. ADD with same IP (different wg key) → ErrDuplicateMeshIP
//   3. REMOVE + ADD with new IP works (the documented rotation path)
//   4. None of the failed ADDs advance the state root (gate rejects
//      before appendOrUpdate runs)
//
// Why this matters: a federated meetup host needs to rotate mesh IPs
// (WiFi network change, VPN reconnect, address reassignment). If the
// protocol silently accepted re-ADD with a new IP, two hosts could
// end up with different IP views of the same wg key — divergence.
// The strict rejection forces the explicit REMOVE+ADD dance that
// produces a clear log trail.
package sim_test

import (
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	"github.com/wscoble/federated-meetup/internal/types"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// bytesEqual is a tiny helper for comparing byte slices in the
// mesh peer rotation tests. Using bytes.Equal from the stdlib is fine
// but a local helper keeps the assertion lines readable.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// meshPeerIPRotationSetup builds a 4-host world with a 1-steward-pair
// group whose initial mesh peer is seedPeer. Returns the world, group ID,
// steward KPs, seed peer KP, and seed peer seed — enough to build
// ADD/REMOVE transitions in the test body (signCosigner needs the seed).
func meshPeerIPRotationSetup(t *testing.T, seed uint64) (*sim.World, types.GroupID, []crypto.KeyPair, ed25519.PublicKey, [32]byte) {
	t.Helper()
	w, err := sim.NewWorld(sim.Config{
		Seed:        seed,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	// Steward signing keys.
	stewardSeeds := []uint64{w.DeriveSeed("alice-rot"), w.DeriveSeed("bob-rot")}
	stewards := make([]crypto.KeyPair, 2)
	for i, s := range stewardSeeds {
		var sd [32]byte
		for j := 0; j < 8; j++ {
			sd[j] = byte(s >> (8 * j))
		}
		stewards[i] = crypto.KeyPairFromSeed(sd)
	}

	// Group keypair.
	var groupSeed [32]byte
	gid := w.DeriveSeed("ip-rotation-group")
	for j := 0; j < 8; j++ {
		groupSeed[j] = byte(gid >> (8 * j))
	}
	groupKP := crypto.KeyPairFromSeed(groupSeed)

	// Seed peer (cosigner for first ADD).
	var seedPeerSeed [32]byte
	sp := w.DeriveSeed("seed-peer-rot")
	for j := 0; j < 8; j++ {
		seedPeerSeed[j] = byte(sp >> (8 * j))
	}
	seedPeerKP := crypto.KeyPairFromSeed(seedPeerSeed)
	seedPeerCoSigner := ed25519.NewKeyFromSeed(seedPeerSeed[:]).Public().(ed25519.PublicKey)
	seedPeerMeshIP := []byte{10, 0, 0, 1}

	// CREATE_GROUP with initial_mesh_peers=[seedPeer].
	createPayload := &pb.CreateGroupPayload{
		CanonicalName:   "ip-rotation",
		DisplayName:     "IP Rotation Group",
		InitialStewards: stewardPBs(stewards),
		Threshold:       2,
		InitialMeshPeers: []*pb.InitialMeshPeer{
			{
				HostWgKey:   seedPeerKP.Public[:],
				MeshIp:      seedPeerMeshIP,
				CosignerKey: seedPeerCoSigner,
			},
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
		Type:     pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload:  &pb.Transition_CreateGroup{CreateGroup: createPayload},
		SignedAt: timestamppb.New(w.Now()),
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
	return w, groupKP.Public, stewards, seedPeerCoSigner, seedPeerSeed
}

func TestMeshPeer_IPRotation_ReAddSameKeyRejected(t *testing.T) {
	w, gid, stewards, seedPeerCoSigner, seedPeerSeed := meshPeerIPRotationSetup(t, 81)
	defer w.Close()

	// Target peer (will be added, then attempted to be re-added).
	var targetPeerSeed [32]byte
	tp := w.DeriveSeed("target-rot-1")
	for j := 0; j < 8; j++ {
		targetPeerSeed[j] = byte(tp >> (8 * j))
	}
	targetPeerKP := crypto.KeyPairFromSeed(targetPeerSeed)
	originalIP := []byte{10, 0, 0, 2}
	rotatedIP := []byte{10, 0, 0, 3}

	// Step 1: ADD target with original IP.
	parentRoot := w.Hosts()[0].State(gid).Root()
	addPayload := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: targetPeerKP.Public[:]},
		MeshIp:          originalIP,
		CosignerPeerKey: &pb.PublicKey{Raw: seedPeerCoSigner},
	}
	cosigCanonical, _ := proto.MarshalOptions{Deterministic: true}.Marshal(addPayload)
	addPayload.CosignerPeerSignature = &pb.Signature{Raw: signCosigner(seedPeerSeed, cosigCanonical)}
	addTr, err := buildSignedTransitionForAddPeer(t, gid, parentRoot, addPayload, stewards, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gid, addTr); err != nil {
			t.Fatalf("first ADD on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterFirstAdd := w.Hosts()[0].State(gid).Root()
	if rootAfterFirstAdd == parentRoot {
		t.Fatal("first ADD did not advance root")
	}

	// Step 2: try to ADD the same wg key with a NEW IP. Must be rejected
	// with ErrDuplicateMeshPeer because the registry is keyed on wg key.
	badAdd := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: targetPeerKP.Public[:]},
		MeshIp:          rotatedIP,
		CosignerPeerKey: &pb.PublicKey{Raw: seedPeerCoSigner},
	}
	cosigCanonical2, _ := proto.MarshalOptions{Deterministic: true}.Marshal(badAdd)
	badAdd.CosignerPeerSignature = &pb.Signature{Raw: signCosigner(seedPeerSeed, cosigCanonical2)}
	badTr, err := buildSignedTransitionForAddPeer(t, gid, rootAfterFirstAdd, badAdd, stewards, 2)
	if err != nil {
		t.Fatal(err)
	}

	h0 := w.Hosts()[0]
	if _, err := h0.SubmitTransition(gid, badTr); err == nil {
		t.Fatal("ADD with same wg key but different IP should be rejected")
	} else if !errors.Is(err, group.ErrDuplicateMeshPeer) {
		t.Fatalf("expected ErrDuplicateMeshPeer, got: %v", err)
	}

	// Root must NOT have advanced (gate rejected before appendOrUpdate).
	if got := h0.State(gid).Root(); got != rootAfterFirstAdd {
		t.Fatalf("rejected ADD advanced root: was %x, now %x",
			rootAfterFirstAdd[:4], got[:4])
	}
	t.Logf("re-ADD with same wg key + different IP correctly rejected (ErrDuplicateMeshPeer); root unchanged")
}

func TestMeshPeer_IPRotation_ReUseIPRejected(t *testing.T) {
	w, gid, stewards, seedPeerCoSigner, seedPeerSeed := meshPeerIPRotationSetup(t, 82)
	defer w.Close()

	// First target peer at IP 10.0.0.2.
	var target1Seed [32]byte
	t1 := w.DeriveSeed("target-rot-2a")
	for j := 0; j < 8; j++ {
		target1Seed[j] = byte(t1 >> (8 * j))
	}
	target1KP := crypto.KeyPairFromSeed(target1Seed)
	sharedIP := []byte{10, 0, 0, 2}

	// Second target peer — different wg key, will try to use same IP.
	var target2Seed [32]byte
	t2 := w.DeriveSeed("target-rot-2b")
	for j := 0; j < 8; j++ {
		target2Seed[j] = byte(t2 >> (8 * j))
	}
	target2KP := crypto.KeyPairFromSeed(target2Seed)

	// Add target1 at sharedIP.
	parentRoot := w.Hosts()[0].State(gid).Root()
	add1 := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: target1KP.Public[:]},
		MeshIp:          sharedIP,
		CosignerPeerKey: &pb.PublicKey{Raw: seedPeerCoSigner},
	}
	cosig1, _ := proto.MarshalOptions{Deterministic: true}.Marshal(add1)
	add1.CosignerPeerSignature = &pb.Signature{Raw: signCosigner(seedPeerSeed, cosig1)}
	add1Tr, _ := buildSignedTransitionForAddPeer(t, gid, parentRoot, add1, stewards, 2)
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gid, add1Tr); err != nil {
			t.Fatalf("ADD target1: %v", err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterAdd1 := w.Hosts()[0].State(gid).Root()

	// Try to ADD target2 with the SAME IP. Must be rejected with
	// ErrDuplicateMeshIP because the registry tracks by IP too.
	add2 := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: target2KP.Public[:]},
		MeshIp:          sharedIP,
		CosignerPeerKey: &pb.PublicKey{Raw: seedPeerCoSigner},
	}
	cosig2, _ := proto.MarshalOptions{Deterministic: true}.Marshal(add2)
	add2.CosignerPeerSignature = &pb.Signature{Raw: signCosigner(seedPeerSeed, cosig2)}
	add2Tr, err := buildSignedTransitionForAddPeer(t, gid, rootAfterAdd1, add2, stewards, 2)
	if err != nil {
		t.Fatal(err)
	}

	h0 := w.Hosts()[0]
	if _, err := h0.SubmitTransition(gid, add2Tr); err == nil {
		t.Fatal("ADD with reused IP should be rejected")
	} else if !errors.Is(err, group.ErrDuplicateMeshIP) {
		t.Fatalf("expected ErrDuplicateMeshIP, got: %v", err)
	}
	if got := h0.State(gid).Root(); got != rootAfterAdd1 {
		t.Fatalf("rejected ADD advanced root: was %x, now %x",
			rootAfterAdd1[:4], got[:4])
	}
	t.Logf("ADD with reused IP correctly rejected (ErrDuplicateMeshIP); root unchanged")
}

func TestMeshPeer_IPRotation_RemoveAddWorkflow(t *testing.T) {
	w, gid, stewards, seedPeerCoSigner, seedPeerSeed := meshPeerIPRotationSetup(t, 83)
	defer w.Close()

	// Target peer will rotate from 10.0.0.2 → 10.0.0.3.
	var targetSeed [32]byte
	tp := w.DeriveSeed("target-rot-3")
	for j := 0; j < 8; j++ {
		targetSeed[j] = byte(tp >> (8 * j))
	}
	targetKP := crypto.KeyPairFromSeed(targetSeed)
	originalIP := []byte{10, 0, 0, 2}
	rotatedIP := []byte{10, 0, 0, 3}

	// Step 1: ADD with originalIP.
	parentRoot := w.Hosts()[0].State(gid).Root()
	add1 := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: targetKP.Public[:]},
		MeshIp:          originalIP,
		CosignerPeerKey: &pb.PublicKey{Raw: seedPeerCoSigner},
	}
	cosig1, _ := proto.MarshalOptions{Deterministic: true}.Marshal(add1)
	add1.CosignerPeerSignature = &pb.Signature{Raw: signCosigner(seedPeerSeed, cosig1)}
	add1Tr, _ := buildSignedTransitionForAddPeer(t, gid, parentRoot, add1, stewards, 2)
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gid, add1Tr); err != nil {
			t.Fatalf("ADD target: %v", err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterAdd := w.Hosts()[0].State(gid).Root()

	// Verify peer is registered with originalIP.
	for _, h := range w.Hosts() {
		peers := h.State(gid).MeshPeers()
		var found bool
		for _, p := range peers {
			if bytesEqual(p.HostWGKey.GetRaw(), targetKP.Public[:]) && string(p.MeshIP) == string(originalIP) {
				found = true
			}
		}
		if !found {
			t.Fatalf("host %s: target peer not registered at originalIP", h.ID())
		}
	}

	// Step 2: REMOVE at originalIP.
	removePayload := &pb.RemoveHostPeerPayload{
		HostWgKey: &pb.PublicKey{Raw: targetKP.Public[:]},
		MeshIp:    originalIP,
	}
	removeTr, _ := buildSignedTransitionForRemovePeer(t, gid, rootAfterAdd, removePayload, stewards, 2)
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gid, removeTr); err != nil {
			t.Fatalf("REMOVE on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterRemove := w.Hosts()[0].State(gid).Root()
	if rootAfterRemove == rootAfterAdd {
		t.Fatal("REMOVE did not advance root")
	}

	// Step 3: ADD with rotatedIP. Must succeed (REMOVE freed the slot).
	add2 := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: targetKP.Public[:]},
		MeshIp:          rotatedIP,
		CosignerPeerKey: &pb.PublicKey{Raw: seedPeerCoSigner},
	}
	cosig2, _ := proto.MarshalOptions{Deterministic: true}.Marshal(add2)
	add2.CosignerPeerSignature = &pb.Signature{Raw: signCosigner(seedPeerSeed, cosig2)}
	add2Tr, _ := buildSignedTransitionForAddPeer(t, gid, rootAfterRemove, add2, stewards, 2)
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gid, add2Tr); err != nil {
			t.Fatalf("re-ADD at rotatedIP on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	rootAfterRotatedAdd := w.Hosts()[0].State(gid).Root()
	if rootAfterRotatedAdd == rootAfterRemove {
		t.Fatal("re-ADD at rotatedIP did not advance root")
	}

	// Verify peer is now at rotatedIP on all hosts.
	for _, h := range w.Hosts() {
		peers := h.State(gid).MeshPeers()
		var foundOrig, foundRotated bool
		for _, p := range peers {
			if !bytesEqual(p.HostWGKey.GetRaw(), targetKP.Public[:]) {
				continue
			}
			if string(p.MeshIP) == string(originalIP) {
				foundOrig = true
			}
			if string(p.MeshIP) == string(rotatedIP) {
				foundRotated = true
			}
		}
		if foundOrig {
			t.Errorf("host %s still has peer at originalIP after rotation", h.ID())
		}
		if !foundRotated {
			t.Errorf("host %s missing peer at rotatedIP after rotation", h.ID())
		}
	}

	// Convergence check.
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gid).Root(); got != rootAfterRotatedAdd {
			t.Fatalf("post-rotation divergence: host %s=%x want %x",
				h.ID(), got[:4], rootAfterRotatedAdd[:4])
		}
	}
	t.Logf("REMOVE+ADD rotation workflow succeeded; all 4 hosts converged at %x",
		rootAfterRotatedAdd[:4])
}