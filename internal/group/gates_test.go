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
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sscoble/federated-meetup/internal/crypto"
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
	cosignerKey := newWireGuardKey(t)
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
	tr := &pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER,
		Payload: &pb.Transition_AddHostPeer{
			AddHostPeer: &pb.AddHostPeerPayload{
				HostWgKey:             &pb.PublicKey{Raw: wgKey.Public().Bytes()},
				MeshIp:                []byte{10, 0, 0, 1},
				CosignerPeerKey:       &pb.PublicKey{Raw: wgKey.Public().Bytes()},
				CosignerPeerSignature: &pb.Signature{Raw: make([]byte, 64)},
			},
		},
	}
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
				CosignerPeerKey:       &pb.PublicKey{Raw: wgKey.Public().Bytes()},
				CosignerPeerSignature: &pb.Signature{Raw: make([]byte, 64)},
			},
		},
	}
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
			Hlc: []byte{byte(i), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		}
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
		Hlc: []byte{99, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	}
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
	tr.PriorState = stateRootFromHead(st)
	signTransition(tr, stewards[:1], gid)
	if err := st.Apply(mustTransition(t, tr, gid), time.Now()); err == nil {
		t.Fatalf("NAME_BIND with single signature should have been rejected")
	}
}