// SPDX-License-Identifier: AGPL-3.0
//
// Integration test for the federation syncer.
//
// This is the "does federation actually federate" proof. It:
//   1. Starts a host A with a populated group state (CREATE_GROUP +
//      ADD_STEWARD transitions).
//   2. Starts a host B with an empty state for the same group key.
//   3. Runs Syncer.Bootstrap against host A from host B.
//   4. Verifies that host B's state root matches host A's.
//   5. Tests idempotent re-bootstrap.
//   6. Tests Verify (snapshot root comparison).

package federation_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/proto/federated_meetup/v1/federatedmeetupv1connect"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/federation"
	"github.com/wscoble/federated-meetup/internal/group"
	"github.com/wscoble/federated-meetup/internal/hlc"
	"github.com/wscoble/federated-meetup/internal/host"
	"github.com/wscoble/federated-meetup/internal/types"
)

// seedFromStr generates a deterministic 32-byte seed from a string.
func seedFromStr(s string) [32]byte {
	var seed [32]byte
	for i := 0; i < len(s) && i < 32; i++ {
		seed[i] = s[i]
	}
	for i := len(s); i < 32; i++ {
		seed[i] = seed[len(s)-1]
	}
	return seed
}

// setupHost creates a group state, applies a CREATE_GROUP + ADD_STEWARD,
// and serves it via httptest. Returns the state, group keypair, stewards,
// and the httptest.Server.
func setupHost(
	t *testing.T,
	name string,
) (
	groupKP crypto.KeyPair,
	stewards []crypto.KeyPair,
	state *group.State,
	server *httptest.Server,
) {
	t.Helper()

	groupKP = crypto.KeyPairFromSeed(seedFromStr("test-group-key-1234"))

	for _, s := range []string{"alice", "bob", "carol"} {
		stewards = append(stewards, crypto.KeyPairFromSeed(seedFromStr(s)))
	}

	state = group.NewState(groupKP.Public)

	// Apply CREATE_GROUP.
	createT := makeCreateGroup(t, groupKP, stewards, 2, time.Now())
	if err := state.Apply(createT, time.Now()); err != nil {
		t.Fatalf("apply CREATE_GROUP: %v", err)
	}

	// Apply ADD_STEWARD (add "dave", signed by alice + bob).
	dave := crypto.KeyPairFromSeed(seedFromStr("dave"))
	addT := makeAddSteward(t, groupKP, stewards[:2], dave.Public, state.Root(), time.Now())
	if err := state.Apply(addT, time.Now()); err != nil {
		t.Fatalf("apply ADD_STEWARD: %v", err)
	}

	// Serve via ConnectRPC.
	svc := host.NewService(name, state)
	mux := http.NewServeMux()
	path, handler := federatedmeetupv1connect.NewHostServiceHandler(svc)
	mux.Handle(path, handler)

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return
}

// setupEmptyHost creates an empty group state and serves it.
func setupEmptyHost(
	t *testing.T,
	groupKP crypto.KeyPair,
	name string,
) (
	state *group.State,
	server *httptest.Server,
) {
	t.Helper()

	state = group.NewState(groupKP.Public)

	svc := host.NewService(name, state)
	mux := http.NewServeMux()
	path, handler := federatedmeetupv1connect.NewHostServiceHandler(svc)
	mux.Handle(path, handler)

	server = httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return
}

func TestFederation_Bootstrap(t *testing.T) {
	groupKP, _, stateA, serverA := setupHost(t, "hostA")

	stateB, _ := setupEmptyHost(t, groupKP, "hostB")

	syncer := federation.NewSyncer(serverA.URL, groupKP.Public, stateB, nil)

	ctx := context.Background()
	applied, err := syncer.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if applied == 0 {
		t.Fatal("bootstrap applied 0 transitions, expected >0")
	}

	t.Logf("bootstrap applied %d transitions", applied)

	// Verify convergence: state roots must match.
	rootA := stateA.Root()
	rootB := stateB.Root()
	if rootA != rootB {
		t.Fatalf("root mismatch after bootstrap:\n  hostA = %x\n  hostB = %x",
			rootA[:], rootB[:])
	}

	countA := stateA.TransitionCount()
	countB := stateB.TransitionCount()
	if countA != countB {
		t.Fatalf("transition count mismatch: hostA=%d hostB=%d", countA, countB)
	}

	t.Logf("convergence verified: root=%x transitions=%d", rootA[:4], countA)
}

func TestFederation_Verify(t *testing.T) {
	groupKP, _, stateA, serverA := setupHost(t, "hostA")
	_ = stateA // stateA used implicitly via serverA

	stateB, _ := setupEmptyHost(t, groupKP, "hostB")

	syncer := federation.NewSyncer(serverA.URL, groupKP.Public, stateB, nil)

	ctx := context.Background()
	if _, err := syncer.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	if err := syncer.Verify(ctx); err != nil {
		t.Fatalf("verify: %v", err)
	}

	t.Log("verify passed: roots match")
}

func TestFederation_IdempotentBootstrap(t *testing.T) {
	groupKP, _, stateA, serverA := setupHost(t, "hostA")

	stateB, _ := setupEmptyHost(t, groupKP, "hostB")

	syncer := federation.NewSyncer(serverA.URL, groupKP.Public, stateB, nil)

	ctx := context.Background()
	applied1, err := syncer.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}

	applied2, err := syncer.Bootstrap(ctx)
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}

	if applied2 != 0 {
		t.Fatalf("second bootstrap applied %d (expected 0 — idempotent)", applied2)
	}

	rootA := stateA.Root()
	rootB := stateB.Root()
	if rootA != rootB {
		t.Fatalf("root mismatch after double bootstrap")
	}

	t.Logf("idempotent: first=%d, second=%d, roots match", applied1, applied2)
}

// TestFederation_LiveStream proves real-time federation: host A applies
// a new transition AFTER host B has bootstrapped. Host B's Syncer.Live
// goroutine receives it via Subscribe and applies it, converging.
func TestFederation_LiveStream(t *testing.T) {
	groupKP, stewards, stateA, serverA := setupHost(t, "hostA")

	stateB, _ := setupEmptyHost(t, groupKP, "hostB")

	syncer := federation.NewSyncer(serverA.URL, groupKP.Public, stateB, nil)
	syncer.SetReconnectDelay(200 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Bootstrap first.
	if _, err := syncer.Bootstrap(ctx); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}

	// Verify convergence after bootstrap.
	rootA := stateA.Root()
	rootB := stateB.Root()
	if rootA != rootB {
		t.Fatalf("root mismatch after bootstrap")
	}
	countA := stateA.TransitionCount()
	t.Logf("bootstrap converged: count=%d root=%x", countA, rootA[:4])

	// Start the live stream in a goroutine.
	go func() {
		if err := syncer.Live(ctx); err != nil && ctx.Err() == nil {
			t.Logf("live stream ended: %v", err)
		}
	}()

	// Give the Subscribe stream a moment to connect.
	time.Sleep(300 * time.Millisecond)

	// Apply a new transition on host A (ADD_STEWARD for "eve").
	eve := crypto.KeyPairFromSeed(seedFromStr("eve"))
	newT := makeAddSteward(t, groupKP, stewards[:2], eve.Public, stateA.Root(), time.Now())
	if err := stateA.Apply(newT, time.Now()); err != nil {
		t.Fatalf("hostA apply ADD_STEWARD: %v", err)
	}
	t.Logf("hostA applied new transition, count=%d", stateA.TransitionCount())

	// Wait for host B to receive and apply it via the live stream.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if stateB.TransitionCount() == stateA.TransitionCount() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify final convergence.
	countA2 := stateA.TransitionCount()
	countB2 := stateB.TransitionCount()
	if countA2 != countB2 {
		t.Fatalf("transition count mismatch after live: hostA=%d hostB=%d", countA2, countB2)
	}

	rootA2 := stateA.Root()
	rootB2 := stateB.Root()
	if rootA2 != rootB2 {
		t.Fatalf("root mismatch after live stream:\n  hostA = %x\n  hostB = %x",
			rootA2[:], rootB2[:])
	}

	t.Logf("live stream converged: count=%d root=%x", countA2, rootA2[:4])
}

// ----- Transition builders -----

func makeCreateGroup(
	t *testing.T,
	groupKP crypto.KeyPair,
	stewards []crypto.KeyPair,
	threshold uint32,
	now time.Time,
) *group.Transition {
	t.Helper()

	stewardPBs := make([]*pb.PublicKey, len(stewards))
	for i, s := range stewards {
		stewardPBs[i] = &pb.PublicKey{Raw: s.Public[:]}
	}

	payload := &pb.CreateGroupPayload{
		CanonicalName:   "test-group",
		DisplayName:     "Test Group",
		InitialStewards: stewardPBs,
		Threshold:       threshold,
	}

	canonical, err := group.MarshalCanonicalForSigningHelper(&pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		PriorState: nil,
		Payload:    &pb.Transition_CreateGroup{CreateGroup: payload},
		SignedAt:   timestamppb.New(now),
	})
	if err != nil {
		t.Fatalf("marshal canonical: %v", err)
	}

	sigs := make([]*pb.Signature, 0, threshold)
	for i := uint32(0); i < threshold; i++ {
		s := crypto.Sign(stewards[i], groupKP.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}

	tr, err := group.NewTransition(&pb.Transition{
		Type:              pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload:           &pb.Transition_CreateGroup{CreateGroup: payload},
		PriorState:         nil,
		SignedAt:           timestamppb.New(now),
		StewardSignatures:  &pb.Multisig{Threshold: threshold, Signatures: sigs},
	}, groupKP.Public)
	if err != nil {
		t.Fatalf("new transition: %v", err)
	}
	return tr
}

func makeAddSteward(
	t *testing.T,
	groupKP crypto.KeyPair,
	signers []crypto.KeyPair,
	newSteward types.PublicKey,
	priorState types.Hash,
	now time.Time,
) *group.Transition {
	t.Helper()

	payload := &pb.AddStewardPayload{
		NewSteward: &pb.PublicKey{Raw: newSteward[:]},
	}

	// HLC for the transition (required for non-CREATE_GROUP).
	hlcBytes := hlc.New(now)

	canonical, err := group.MarshalCanonicalForSigningHelper(&pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		PriorState: &pb.StateRoot{Hash: priorState[:]},
		Payload:    &pb.Transition_AddSteward{AddSteward: payload},
		SignedAt:   timestamppb.New(now),
		Hlc:        hlcBytes,
	})
	if err != nil {
		t.Fatalf("marshal canonical: %v", err)
	}

	sigs := make([]*pb.Signature, 0, len(signers))
	for _, k := range signers {
		s := crypto.Sign(k, groupKP.Public, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}

	tr, err := group.NewTransition(&pb.Transition{
		Type:              pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		Payload:           &pb.Transition_AddSteward{AddSteward: payload},
		PriorState:         &pb.StateRoot{Hash: priorState[:]},
		SignedAt:           timestamppb.New(now),
		Hlc:                hlcBytes,
		StewardSignatures:  &pb.Multisig{Threshold: 2, Signatures: sigs},
	}, groupKP.Public)
	if err != nil {
		t.Fatalf("new transition: %v", err)
	}
	return tr
}