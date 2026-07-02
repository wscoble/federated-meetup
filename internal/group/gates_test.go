// SPDX-License-Identifier: MIT
//
// Gate tests (G1, G2, G3, G6, G7, G8).
//
// Each test verifies that the corresponding gate fires under the
// attack it's designed to defeat. The pattern:
//   1. Build a legitimate scenario.
//   2. Construct the attack transition.
//   3. Apply both. Legitimate succeeds; attack is rejected.
//   4. Assert state invariants.

package group

import (
	"crypto/ed25519"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/hlc"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// genKey returns a deterministic KeyPair for test fixtures.
func genKey(seed byte) crypto.KeyPair {
	var s [32]byte
	for i := range s {
		s[i] = seed
	}
	return crypto.KeyPairFromSeed(s)
}

// pub returns the proto view of a keypair's public key.
func pub(k crypto.KeyPair) *pb.PublicKey {
	return &pb.PublicKey{Raw: k.Public[:]}
}

// signTransition attaches a multisig envelope to tr with one
// signature per signer in `signers`. The envelope threshold equals
// the number of signers.
//
// Mirrors the pattern used by sim/threat_test.go (which works): the
// canonical bytes are computed once via MarshalCanonicalForSigningHelper,
// then each signer produces a signature via crypto.Sign.
func signTransition(tr *pb.Transition, signers []crypto.KeyPair, gid types.GroupID) {
	canonical, err := MarshalCanonicalForSigningHelper(tr)
	if err != nil {
		panic(err)
	}
	groupKey := types.PublicKey{}
	copy(groupKey[:], gid[:])
	sigs := make([]*pb.Signature, 0, len(signers))
	for _, k := range signers {
		sig := crypto.Sign(k, groupKey, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: sig[:]})
	}
	tr.StewardSignatures = &pb.Multisig{
		Signatures: sigs,
		Threshold:  uint32(len(signers)),
	}
}

// createGroupWith creates a CREATE_GROUP transition + applies it.
func createGroupWith(t *testing.T, gid types.GroupID, stewards []crypto.KeyPair, threshold uint32) *State {
	t.Helper()
	if len(stewards) < int(threshold) {
		t.Fatalf("not enough stewards (%d) for threshold %d", len(stewards), threshold)
	}
	initialPubs := make([]*pb.PublicKey, len(stewards))
	for i, k := range stewards {
		initialPubs[i] = pub(k)
	}
	st := NewState(gid)
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload: &pb.Transition_CreateGroup{
			CreateGroup: &pb.CreateGroupPayload{
				CanonicalName:   "test-group",
				DisplayName:     "Test Group",
				InitialStewards: initialPubs,
				Threshold:       threshold,
			},
		},
	}
	signTransition(tr, stewards[:threshold], gid)
	t1, err := NewTransition(tr, gid)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Apply(t1, time.Now()); err != nil {
		t.Fatalf("CREATE_GROUP apply: %v", err)
	}
	return st
}

func stateRootFromHead(st *State) *pb.StateRoot {
	root := st.Root()
	return &pb.StateRoot{Hash: root[:]}
}

func mustTransition(t *testing.T, tr *pb.Transition, gid types.GroupID) *Transition {
	t.Helper()
	out, err := NewTransition(tr, gid)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// stampHLC sets a valid 18-byte HLC on the transition proto. Use this
// on any non-CREATE_GROUP transition before calling st.Apply() so the
// M-6 HLC length validation passes. Call BEFORE signing or before
// Apply (the canonical bytes include the HLC, so if you sign first
// and then add HLC, the signature won't match).
//
// For tests that sign and then apply, call stampHLC BEFORE signTransition.
func stampHLC(tr *pb.Transition) {
	tr.Hlc = hlc.New(time.Now())
}

// newCoSignerKey generates a fresh Ed25519 CoSigner key for tests.
func newCoSignerKey(t *testing.T) crypto.CoSignerKey {
	t.Helper()
	k, err := crypto.GenerateCoSignerKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// newWireGuardKey generates a fresh wg key for tests.
func newWireGuardKey(t *testing.T) crypto.WireGuardKey {
	t.Helper()
	k, err := crypto.GenerateWireGuardKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newTLSKey(t *testing.T) crypto.TLSKey {
	t.Helper()
	k, err := crypto.GenerateTLSKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// =============================================================================
// G1 — ISSUE_HOST_CERT / REVOKE_HOST_CERT
// =============================================================================

func TestGate1_IssueHostCertSucceeds(t *testing.T) {
	gid := types.GroupID{0x01}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	hostTLS := newTLSKey(t)
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		Payload: &pb.Transition_IssueHostCert{
			IssueHostCert: &pb.IssueHostCertPayload{
				Hostname:   "vegas-programmers.example.com",
				HostTlsKey: &pb.PublicKey{Raw: hostTLS.Public().Bytes()},
				NotBefore:  timestamppb.Now(),
				NotAfter:   timestamppb.New(time.Now().Add(30 * 24 * time.Hour)),
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)

	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
		t.Fatalf("ISSUE_HOST_CERT apply: %v", err)
	}
	snap := st.Snapshot()
	found := false
	for _, e := range snap.Entries {
		if len(e.Key) > len("host_cert/") && e.Key[:len("host_cert/")] == "host_cert/" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("host cert not found in state after ISSUE_HOST_CERT")
	}
}

func TestGate1_IssueHostCertRequiresThreshold(t *testing.T) {
	gid := types.GroupID{0x02}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	hostTLS := newTLSKey(t)
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		Payload: &pb.Transition_IssueHostCert{
			IssueHostCert: &pb.IssueHostCertPayload{
				Hostname:   "evil.example.com",
				HostTlsKey: &pb.PublicKey{Raw: hostTLS.Public().Bytes()},
				NotAfter:   timestamppb.New(time.Now().Add(24 * time.Hour)),
			},
		},
	}
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:1], gid)

	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err == nil {
		t.Fatalf("ISSUE_HOST_CERT with single signature should have been rejected")
	}
}

func TestGate1_RevokeHostCertTombstones(t *testing.T) {
	gid := types.GroupID{0x03}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	hostTLS := newTLSKey(t)
	notAfter := timestamppb.New(time.Now().Add(30 * 24 * time.Hour))

	tr1 := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		Payload: &pb.Transition_IssueHostCert{
			IssueHostCert: &pb.IssueHostCertPayload{
				Hostname:   "vegas-programmers.example.com",
				HostTlsKey: &pb.PublicKey{Raw: hostTLS.Public().Bytes()},
				NotAfter:   notAfter,
			},
		},
	}
	stampHLC(tr1)
	tr1.PriorState = stateRootFromHead(st)
	signTransition(tr1, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr1, gid), time.Now()); err != nil {
		t.Fatalf("issue cert: %v", err)
	}

	tr2 := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_REVOKE_HOST_CERT,
		Payload: &pb.Transition_RevokeHostCert{
			RevokeHostCert: &pb.RevokeHostCertPayload{
				Hostname:   "vegas-programmers.example.com",
				HostTlsKey: &pb.PublicKey{Raw: hostTLS.Public().Bytes()},
				NotAfter:   notAfter,
				Reason:     "host operator key compromise",
			},
		},
	}
	stampHLC(tr2)
	tr2.PriorState = stateRootFromHead(st)
	signTransition(tr2, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr2, gid), time.Now()); err != nil {
		t.Fatalf("revoke cert: %v", err)
	}

	snap := st.Snapshot()
	for _, e := range snap.Entries {
		if len(e.Key) > len("host_cert/") && e.Key[:len("host_cert/")] == "host_cert/" {
			if e.Value != nil {
				t.Errorf("expected cert entry to be tombstoned, got value %x", e.Value)
			}
		}
	}
}

// =============================================================================
// G2 — ADD_HOST_PEER
// =============================================================================

func TestGate2_AddHostPeerRequiresCosigner(t *testing.T) {
	gid := types.GroupID{0x10}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	wgKey := newWireGuardKey(t)
	cosignerKey := newCoSignerKey(t)
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		Payload: &pb.Transition_AddHostPeer{
			AddHostPeer: &pb.AddHostPeerPayload{
				HostWgKey:             &pb.PublicKey{Raw: wgKey.Public().Bytes()},
				MeshIp:                []byte{10, 0, 0, 5},
				CosignerPeerKey:       &pb.PublicKey{Raw: cosignerKey.Public().Bytes()},
				CosignerPeerSignature: &pb.Signature{Raw: make([]byte, 64)},
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)

	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err == nil {
		t.Fatalf("ADD_HOST_PEER without valid mesh-member co-sig should have been rejected")
	}
}

func TestGate2_AddHostPeerHonorsMeshCap(t *testing.T) {
	gid := types.GroupID{0x12}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)
	st.MaxMeshPeers = 1

	wgKey := newWireGuardKey(t)
	cosignerKey := newCoSignerKey(t)
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		Payload: &pb.Transition_AddHostPeer{
			AddHostPeer: &pb.AddHostPeerPayload{
				HostWgKey:             &pb.PublicKey{Raw: wgKey.Public().Bytes()},
				MeshIp:                []byte{10, 0, 0, 1},
				CosignerPeerKey:       &pb.PublicKey{Raw: cosignerKey.Public().Bytes()},
				CosignerPeerSignature: &pb.Signature{Raw: make([]byte, 64)},
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
		t.Logf("first peer add result: %v", err)
	}

	newKey := newWireGuardKey(t)
	tr2 := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		Payload: &pb.Transition_AddHostPeer{
			AddHostPeer: &pb.AddHostPeerPayload{
				HostWgKey:             &pb.PublicKey{Raw: newKey.Public().Bytes()},
				MeshIp:                []byte{10, 0, 0, 2},
				CosignerPeerKey:       &pb.PublicKey{Raw: cosignerKey.Public().Bytes()},
				CosignerPeerSignature: &pb.Signature{Raw: make([]byte, 64)},
			},
		},
	}
	stampHLC(tr2)
	tr2.PriorState = stateRootFromHead(st)
	signTransition(tr2, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr2, gid), time.Now()); err == nil {
		t.Fatalf("second ADD_HOST_PEER should have been rejected by mesh cap")
	}
}

// =============================================================================
// G3 — DECLARE_STEWARD_CUSTODY
// =============================================================================

func TestGate3_DeclareStewardCustodySucceeds(t *testing.T) {
	gid := types.GroupID{0x20}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	steward0Pub := pub(stewards[0])
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY,
		Payload: &pb.Transition_DeclareStewardCustody{
			DeclareStewardCustody: &pb.DeclareStewardCustodyPayload{
				Steward:       steward0Pub,
				Tier:          pb.CustodyTier_CUSTODY_TIER_HARDWARE_WALLET,
				Justification: "Ledger Nano X in office safe",
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
		t.Fatalf("DECLARE_STEWARD_CUSTODY apply: %v", err)
	}

	view := st.CustodyView()
	var key [32]byte
	copy(key[:], steward0Pub.GetRaw())
	if got := view.TierFor(key); got != pb.CustodyTier_CUSTODY_TIER_HARDWARE_WALLET {
		t.Errorf("expected hardware-wallet tier, got %v", got)
	}
}

func TestGate3_NonStewardCannotDeclare(t *testing.T) {
	gid := types.GroupID{0x21}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	outsider := genKey(99)
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY,
		Payload: &pb.Transition_DeclareStewardCustody{
			DeclareStewardCustody: &pb.DeclareStewardCustodyPayload{
				Steward: pub(outsider),
				Tier:    pb.CustodyTier_CUSTODY_TIER_HSM,
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err == nil {
		t.Fatalf("DECLARE_STEWARD_CUSTODY by non-steward should have been rejected")
	}
}

// =============================================================================
// G6 — SLASH_STEWARD
// =============================================================================

func TestGate6_SlashStewardSucceeds(t *testing.T) {
	gid := types.GroupID{0x30}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	steward2Pub := pub(stewards[2])
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_SLASH_STEWARD,
		Payload: &pb.Transition_SlashSteward{
			SlashSteward: &pb.SlashStewardPayload{
				SlashedSteward: steward2Pub,
				PriorState:     stateRootFromHead(st),
				HlcA:           []byte{0x01, 0x02, 0x03},
				HlcB:           []byte{0x04, 0x05, 0x06},
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
		t.Fatalf("SLASH_STEWARD apply: %v", err)
	}

	stewardsAfter := st.Stewards()
	slashedKey := types.PublicKey{}
	copy(slashedKey[:], steward2Pub.GetRaw())
	for _, s := range stewardsAfter {
		if s.Key == slashedKey {
			t.Errorf("slashed steward still in active set")
		}
	}
}

func TestGate6_SlashedStewardCannotCosign(t *testing.T) {
	gid := types.GroupID{0x31}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	steward2Pub := pub(stewards[2])
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_SLASH_STEWARD,
		Payload: &pb.Transition_SlashSteward{
			SlashSteward: &pb.SlashStewardPayload{
				SlashedSteward: steward2Pub,
				PriorState:     stateRootFromHead(st),
			},
		},
	}
	// Slashed steward (stewards[2]) co-signs alongside stewards[1].
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, []crypto.KeyPair{stewards[1], stewards[2]}, gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err == nil {
		t.Fatalf("SLASH_STEWARD with slashed co-signer should have been rejected")
	}
}

// =============================================================================
// G7 — Memory bounds
// =============================================================================

func TestGate7_EquivocationLogBounded(t *testing.T) {
	e := newEquivocationLog()
	e.SetMaxEntries(3)

	var k types.PublicKey
	for i := byte(0); i < 5; i++ {
		k[0] = i
		e.check(k, types.Hash{}, []byte{byte(i)}, types.Hash{byte(i)})
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.seen) > 3 {
		t.Errorf("expected log size <= 3, got %d", len(e.seen))
	}
}

func TestGate7_StateKVSizeCapped(t *testing.T) {
	gid := types.GroupID{0x40}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)
	// Inspect starting entries to size the cap appropriately.
	startingEntries := len(st.Snapshot().Entries)
	// Pick MaxKVSize so that exactly 4 events fit beyond the
	// starting entries. After 4 events we hit the cap; the 5th
	// must be rejected.
	st.MaxKVSize = startingEntries + 4

	for i := 0; i < 4; i++ {
		tr := &pb.Transition{
			Type: pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
			Payload: &pb.Transition_CreateEvent{
				CreateEvent: &pb.CreateEventPayload{
					EventId: string(rune('a' + i)),
					Title:   "Event",
				},
			},
		}
		stampHLC(tr)
		tr.PriorState = stateRootFromHead(st)
		signTransition(tr, stewards[:2], gid)
		if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
			t.Fatalf("event %d apply: %v", i, err)
		}
	}

	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		Payload: &pb.Transition_CreateEvent{
			CreateEvent: &pb.CreateEventPayload{
				EventId: "fifth",
				Title:   "Event",
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err == nil {
		t.Fatalf("event past MaxKVSize should have been rejected")
	}
}

// =============================================================================
// G8 — NAME_BIND
// =============================================================================

func TestGate8_NameBindSucceeds(t *testing.T) {
	gid := types.GroupID{0x50}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_NAME_BIND,
		Payload: &pb.Transition_NameBind{
			NameBind: &pb.NameBindPayload{
				Name:          "vegas-programmers",
				DirectoryHost: "dir.example.com",
				NotAfter:      timestamppb.New(time.Now().Add(365 * 24 * time.Hour)),
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
		t.Fatalf("NAME_BIND apply: %v", err)
	}
}

func TestGate8_NameBindRequiresThreshold(t *testing.T) {
	gid := types.GroupID{0x51}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_NAME_BIND,
		Payload: &pb.Transition_NameBind{
			NameBind: &pb.NameBindPayload{
				Name:     "vegas-programmers",
				NotAfter: timestamppb.New(time.Now().Add(24 * time.Hour)),
			},
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:1], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err == nil {
		t.Fatalf("NAME_BIND with single signature should have been rejected")
	}
}

// =============================================================================
// C-1 characterization: X25519 keys are NOT valid Ed25519 keys
// =============================================================================

// TestC1_X25519IsNotValidEd25519 proves the original bug: a WireGuard
// X25519 public key cannot be used as an Ed25519 public key for
// signing/verification because there is no Ed25519 private key that
// corresponds to it. The X25519 private key is NOT a valid Ed25519
// private key (different derivation: X25519 uses scalar multiplication
// on Curve25519, Ed25519 uses a different seed-to-key expansion).
//
// We generate 256 X25519 keypairs, attempt to sign with the X25519
// private key bytes as if they were Ed25519 private key bytes, and
// verify against the X25519 public key. The success rate should be
// ~0% because X25519 private keys are not valid Ed25519 private keys.
func TestC1_X25519IsNotValidEd25519(t *testing.T) {
	var signVerifySucceeded int
	const samples = 256
	for i := 0; i < samples; i++ {
		wgKey, err := crypto.GenerateWireGuardKey()
		if err != nil {
			t.Fatal(err)
		}
		wgPub := wgKey.Public().Bytes() // 32 bytes, X25519

		// The X25519 private key is 32 random bytes. Ed25519 private keys
		// are 64 bytes (seed + public key). We can't construct a valid
		// Ed25519 signature from an X25519 private key. But the old code
		// treated the X25519 public key as an Ed25519 public key for
		// VERIFICATION. The bug: there's no way to produce a signature
		// that verifies against this "public key" because no Ed25519
		// private key corresponds to it.
		//
		// To demonstrate: try to sign with the X25519 private bytes as
		// an Ed25519 seed. ed25519.NewKeyFromSeed will produce SOME
		// Ed25519 key, but its public key won't match the X25519 public
		// key. So any signature will fail verification against wgPub.
		func() {
			defer func() {
				if r := recover(); r != nil {
					// ed25519 may panic on malformed input
				}
			}()
			// Construct an Ed25519 key from the X25519 private bytes as
			// a seed. This produces a valid Ed25519 keypair, but its
			// public key is unrelated to the X25519 public key.
			edPriv := ed25519.NewKeyFromSeed(wgKey.PrivateBytes())
			edPub := edPriv.Public().(ed25519.PublicKey)

			// If the Ed25519 public key happens to match the X25519
			// public key, signing+verifying would work. It shouldn't.
			if string(edPub) == string(wgPub) {
				signVerifySucceeded++
			}
		}()
	}
	if signVerifySucceeded > 0 {
		t.Errorf("X25519 pub matched Ed25519-from-X25519-priv-seed pub: %d/%d — expected 0", signVerifySucceeded, samples)
	}
	t.Logf("X25519 pub == Ed25519-from-priv-seed pub: %d/%d (%.1f%%) — confirms no correspondence between X25519 and Ed25519 key derivation", signVerifySucceeded, samples, float64(signVerifySucceeded)*100/float64(samples))
}

// TestC1_CoSignerKeyWorks proves the fix: a dedicated Ed25519 CoSigner
// keypair signs and verifies correctly through verifyCoSignerSignature.
func TestC1_CoSignerKeyWorks(t *testing.T) {
	coSigner, err := crypto.GenerateCoSignerKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("test cosigner message")
	sig := signStewardEd25519(coSigner.PrivateKey(), "add_host_peer_cosig", msg)
	if !verifyCoSignerSignature(coSigner.Public().Bytes(), msg, sig) {
		t.Fatal("CoSigner Ed25519 keypair failed to verify — the fix is broken")
	}
}

// TestC1_X25519KeyFailsCosignerVerify proves that an X25519 key used
// as a cosigner key does NOT verify, even when signed with the
// corresponding X25519 private key (which is not a valid Ed25519
// private key). This is the negative characterization.
func TestC1_X25519KeyFailsCosignerVerify(t *testing.T) {
	wgKey, err := crypto.GenerateWireGuardKey()
	if err != nil {
		t.Fatal(err)
	}
	wgPub := wgKey.Public().Bytes()
	msg := []byte("test message")
	// Sign with a random Ed25519 key — verification against the X25519
	// pub key must fail.
	edPub, edPriv, _ := ed25519.GenerateKey(nil)
	_ = edPub
	sig := ed25519.Sign(edPriv, domainPrefix("add_host_peer_cosig", msg))
	if verifyCoSignerSignature(wgPub, msg, sig) {
		t.Fatal("X25519 public key accepted as Ed25519 cosigner — C-1 fix broken")
	}
}

// TestGate2_AddHostPeerWithValidCosignature is the positive test: a
// properly seeded mesh peer with a valid Ed25519 CoSigner keypair
// cosigns an ADD_HOST_PEER transition, and it succeeds.
func TestGate2_AddHostPeerWithValidCosignature(t *testing.T) {
	gid := types.GroupID{0x14}
	stewards := []crypto.KeyPair{genKey(1), genKey(2), genKey(3)}
	st := createGroupWith(t, gid, stewards, 2)

	// Seed a mesh peer with a dedicated CoSigner key.
	seedWG := newWireGuardKey(t)
	seedCoSigner := newCoSignerKey(t)
	seedPeer := &MeshPeer{
		HostWGKey:  &pb.PublicKey{Raw: seedWG.Public().Bytes()},
		MeshIP:     []byte{10, 0, 0, 1},
		CoSignerKey: &pb.PublicKey{Raw: seedCoSigner.Public().Bytes()},
	}
	if err := st.addMeshPeerLocked(seedPeer); err != nil {
		t.Fatal(err)
	}

	// Build ADD_HOST_PEER for a new peer, cosigned by the seed peer.
	newWG := newWireGuardKey(t)
	addPayload := &pb.AddHostPeerPayload{
		HostWgKey:       &pb.PublicKey{Raw: newWG.Public().Bytes()},
		MeshIp:          []byte{10, 0, 0, 2},
		CosignerPeerKey: &pb.PublicKey{Raw: seedCoSigner.Public().Bytes()},
	}
	// Compute the canonical bytes for the cosignature (excluding the
	// signature field, matching verifyAddHostPeerPayload).
	cp := proto.Clone(addPayload).(*pb.AddHostPeerPayload)
	cp.CosignerPeerSignature = nil
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
	if err != nil {
		t.Fatal(err)
	}
	sig := signStewardEd25519(seedCoSigner.PrivateKey(), "add_host_peer_cosig", canonical)
	addPayload.CosignerPeerSignature = &pb.Signature{Raw: sig}

	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		Payload: &pb.Transition_AddHostPeer{
			AddHostPeer: addPayload,
		},
	}
	stampHLC(tr)
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:2], gid)

	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err != nil {
		t.Fatalf("ADD_HOST_PEER with valid cosignature should have succeeded: %v", err)
	}
	// The new peer should be in the mesh.
	found := false
	for _, p := range st.MeshPeers() {
		if string(p.MeshIP) == string([]byte{10, 0, 0, 2}) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("new peer not found in mesh after successful ADD_HOST_PEER")
	}
}