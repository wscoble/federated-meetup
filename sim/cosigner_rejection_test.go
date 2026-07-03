// SPDX-License-Identifier: AGPL-3.0
//
// Cycle 56 — Negative test for ADD_HOST_PEER cosignature rejection.
//
// Verifies that an ADD_HOST_PEER transition whose cosigner is
// not a registered mesh member (not in the byCoSigner index) is
// rejected. This pins the C-1 fix: the cosigner identity is the
// peer's CoSigner (Ed25519) key, not the wg (X25519) key, and
// the registry lookup is keyed on CoSigner.

package sim_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestAddHostPeer_CosignerNotInMesh_Rejected confirms that an
// ADD_HOST_PEER with a cosigner key that isn't a registered
// mesh member is rejected at the apply switch.
func TestAddHostPeer_CosignerNotInMesh_Rejected(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        92,
		HostCount:   2,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	priorRoot := w.Hosts()[0].State(gkp.Public).Root()

	// Forge a rogue CoSigner key — random Ed25519 key, NOT in any mesh.
	roguePub, roguePriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	// New peer to add (with its own wg key).
	var newPeerSeed [32]byte
	np := w.DeriveSeed("cosigner-reject-newpeer")
	for j := 0; j < 8; j++ {
		newPeerSeed[j] = byte(np >> (8 * j))
	}
	newPeerKP := crypto.KeyPairFromSeed(newPeerSeed)

	addPayload := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: newPeerKP.Public[:]},
		MeshIp:          []byte{10, 0, 0, 99},
		CosignerPeerKey: &pb.PublicKey{Raw: roguePub},
	}
	// Canonical bytes (CosignerPeerSignature nil-ed).
	cp := proto.Clone(addPayload).(*pb.AddHostPeerPayload)
	cp.CosignerPeerSignature = nil
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	// Domain-separated prefix matching gates.go domainPrefix.
	prefixed := cosigDomainPrefix(canonical)
	addPayload.CosignerPeerSignature = &pb.Signature{
		Raw: ed25519.Sign(roguePriv, prefixed),
	}

	proto2 := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		PriorState: &pb.StateRoot{Hash: priorRoot[:]},
		Payload:    &pb.Transition_AddHostPeer{AddHostPeer: addPayload},
		SignedAt:   timestamppb.New(w.Now()),
	}
	canon2, err := group.MarshalCanonicalForSigningHelper(proto2)
	if err != nil {
		t.Fatal(err)
	}
	sigs := sigsFor(stewards, gkp.Public, canon2)
	proto2.StewardSignatures = &pb.Multisig{Threshold: uint32(len(stewards)), Signatures: sigs}
	tx, err := group.NewTransition(proto2, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}

	_, err = w.Hosts()[0].SubmitTransition(gkp.Public, tx)
	if err == nil {
		t.Fatalf("ADD_HOST_PEER with non-member cosigner was ACCEPTED — C-1 fix broken")
	}
	t.Logf("correctly rejected: %v", err)
}

// cosigDomainPrefix mirrors internal/group/gates.go domainPrefix
// for the "add_host_peer_cosig" domain. SHA-256(domain || uint64_be(len(msg)) || msg).
func cosigDomainPrefix(msg []byte) []byte {
	h := sha256.New()
	h.Write([]byte("add_host_peer_cosig"))
	var lenBuf [8]byte
	// big-endian length
	ll := uint64(len(msg))
	for i := 7; i >= 0; i-- {
		lenBuf[i] = byte(ll & 0xff)
		ll >>= 8
	}
	h.Write(lenBuf[:])
	h.Write(msg)
	return h.Sum(nil)
}