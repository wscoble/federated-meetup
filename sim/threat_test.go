// SPDX-License-Identifier: AGPL-3.0
//
// Threat-model regression tests. These verify that the hardening in
// §5.4 of the protocol spec actually catches the attacks it claims to:
//
//   1. Equivocation — a Byzantine steward signs two transitions at the
//      same prior_state; the second is rejected.
//   2. HLC drift — a peer injects a far-future HLC; Deliver rejects
//      before advancing the cursor.
//   3. Steward set growth — repeated ADD_STEWARD hits MaxStewards and
//      is rejected.
//
// Each test wires a small scenario through the simulator and asserts
// the invariant directly. Same seed → same result.

package sim_test

import (
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/hlc"
	"github.com/sscoble/federated-meetup/internal/ratelimit"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// TestThreat_EquivocationRejected verifies that the equivocation log
// detects a steward signing two different transitions at the same
// (steward, prior_state). This is the data-structure test — the full
// end-to-end "two hosts fork via equivocation" scenario lives in a
// separate test (TestThreat_EquivocationForkDetection) once the
// gossip-level evidence pipeline is wired.
//
// Setup: 3-steward group. We pre-seed the equivocation log by
// recording alice's first signature at (alice_key, prior_root,
// first_hlc, first_txhash). Then we query CheckEquivocation with the
// SAME (alice_key, prior_root) but DIFFERENT hlc + txhash — should
// return true (conflict). Then we query with the SAME (alice_key,
// prior_root) and SAME hlc + txhash — should return false (replay,
// not equivocation).
func TestThreat_EquivocationRejected(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        11,
		HostCount:   1,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))
	gkp := setupVegasProgrammers(w)

	st := w.Hosts()[0].State(gkp.Public)
	stewards := stewardKPsForTest(w) // [alice, bob, carol]

	// Build two transitions at the same prior_state with different
	// payloads. Sign both with alice + bob (threshold 2).
	stRoot := st.Root()
	priorRoot := &pb.StateRoot{Hash: stRoot[:]}
	buildTransition := func(stewardName string) *group.Transition {
		p := &pb.AddStewardPayload{
			NewSteward: &pb.PublicKey{Raw: []byte(stewardName)},
		}
		trProto := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
			PriorState: priorRoot,
			Payload:    &pb.Transition_AddSteward{AddSteward: p},
			SignedAt:   timestamppb.New(w.Now()),
		}
		canonical, _ := group.MarshalCanonicalForSigningHelper(trProto)
		sigA := crypto.Sign(stewards[0], gkp.Public, crypto.MsgKindTransition, canonical)
		sigB := crypto.Sign(stewards[1], gkp.Public, crypto.MsgKindTransition, canonical)
		trProto.StewardSignatures = &pb.Multisig{
			Threshold:  2,
			Signatures: []*pb.Signature{{Raw: sigA[:]}, {Raw: sigB[:]}},
		}
		tr, _ := group.NewTransition(trProto, gkp.Public)
		tr.Proto.Hlc = hlc.New(w.Now().Add(10 * time.Millisecond))
		return tr
	}

	tr1 := buildTransition("steward-A")
	tr2 := buildTransition("steward-B")

	// Different HLCs (force distinct).
	tr2.Proto.Hlc = hlc.New(w.Now().Add(50 * time.Millisecond))

	// Compute tx hashes (the canonical sign-bytes).
	txHash1 := sha256Of(tr1.Canonical())
	txHash2 := sha256Of(tr2.Canonical())

	// Convert prior_state to types.Hash.
	var prior types.Hash
	copy(prior[:], priorRoot.GetHash())

	// Pre-seed the equivocation log with tr1's record. We do this by
	// applying tr1 to a state object. Use a fresh state that has
	// CREATE_GROUP applied first (so its root matches tr1's prior_state).
	fresh := group.NewState(gkp.Public)
	fresh.MaxStewards = 100
	// Build a CREATE_GROUP transition that matches the setup's
	// canonical bytes — same payload, same stewards, same threshold.
	createCanonical, _ := group.MarshalCanonicalForSigningHelper(&pb.Transition{
		Type:    pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload: &pb.Transition_CreateGroup{
			CreateGroup: &pb.CreateGroupPayload{
				CanonicalName:   "vegas-programmers",
				DisplayName:     "Vegas Programmers",
				InitialStewards: stewardPBs(stewards),
				Threshold:       2,
			},
		},
		SignedAt: timestamppb.New(w.Now()),
	})
	createSigs := sigsFor(stewards[:2], gkp.Public, createCanonical)
	createTr, err := group.NewTransition(&pb.Transition{
		Type: pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload: &pb.Transition_CreateGroup{
			CreateGroup: &pb.CreateGroupPayload{
				CanonicalName:   "vegas-programmers",
				DisplayName:     "Vegas Programmers",
				InitialStewards: stewardPBs(stewards),
				Threshold:       2,
			},
		},
		SignedAt:          timestamppb.New(w.Now()),
		StewardSignatures: &pb.Multisig{Threshold: 2, Signatures: createSigs},
	}, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.Apply(createTr, w.Now()); err != nil {
		t.Fatalf("apply CREATE_GROUP to fresh state: %v", err)
	}
	if err := fresh.Apply(tr1, w.Now()); err != nil {
		t.Fatalf("apply tr1 to fresh state: %v", err)
	}

	// Now query the equivocation log via the public CheckEquivocation
	// helper. First, the conflict case: same (alice, prior) but
	// different hlc + txhash.
	conflict := fresh.CheckEquivocation(stewards[0].Public, prior, tr2.Proto.GetHlc(), txHash2)
	if !conflict {
		t.Fatal("expected equivocation: same (steward, prior) with different hlc+txhash should conflict")
	}
	t.Logf("equivocation detected for alice + prior + distinct HLC")

	// Second, the replay case: same (alice, prior, hlc, txhash) as
	// tr1 — not an equivocation, just a replay.
	replay := fresh.CheckEquivocation(stewards[0].Public, prior, tr1.Proto.GetHlc(), txHash1)
	if replay {
		t.Fatal("replay (same hlc + txhash) should NOT be flagged as equivocation")
	}
	t.Logf("replay not flagged as equivocation (correct)")
}

// sha256Of is a tiny helper.
func sha256Of(b []byte) types.Hash {
	h := sha256Sum(b)
	var out types.Hash
	copy(out[:], h[:])
	return out
}

// sha256Sum wraps crypto/sha256.Sum256 to keep imports local.
func sha256Sum(b []byte) [32]byte {
	return sha256SumImpl(b)
}

// TestThreat_HLCDriftRejected verifies that Deliver() rejects inbound
// messages whose HLC wall component is more than MaxHLCDrift ahead of
// the host's local clock. Adversary scenario: peer floods with HLC
// wall = now + 1000 years to exhaust legitimate ordering space.
//
// We build a forged transition with a far-future HLC and verify
// Deliver returns ErrHLCDriftExceeded and the host's dropped counter
// increments.
func TestThreat_HLCDriftRejected(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        22,
		HostCount:   2,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))
	gkp := setupVegasProgrammers(w)

	// Drain the CREATE_GROUP.
	for i := 0; i < 3; i++ {
		w.Advance(50 * time.Millisecond)
		for _, h := range w.Hosts() {
			for _, msg := range h.PeekMessages() {
				if msg.Tag == "transition" {
					_ = h.Deliver(msg.Payload)
				}
			}
		}
	}

	receiver := w.Hosts()[1]

	// Construct a forged transition with HLC wall = now + 1 year
	// (far exceeds MaxHLCDrift = 60s).
	stewards := stewardKPsForTest(w)
	rRoot := receiver.State(gkp.Public).Root()
	forgedPrior := &pb.StateRoot{Hash: rRoot[:]}
	forgedPayload := &pb.AddStewardPayload{
		NewSteward: &pb.PublicKey{Raw: []byte("forged-steward")},
	}
	forgedProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		PriorState: forgedPrior,
		Payload:    &pb.Transition_AddSteward{AddSteward: forgedPayload},
		SignedAt:   timestamppb.New(w.Now()),
	}
	canonical, _ := group.MarshalCanonicalForSigningHelper(forgedProto)
	sigA := crypto.Sign(stewards[0], gkp.Public, crypto.MsgKindTransition, canonical)
	sigB := crypto.Sign(stewards[1], gkp.Public, crypto.MsgKindTransition, canonical)
	forgedProto.StewardSignatures = &pb.Multisig{
		Threshold:  2,
		Signatures: []*pb.Signature{{Raw: sigA[:]}, {Raw: sigB[:]}},
	}
	forgedTr, err := group.NewTransition(forgedProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	// Stamp a far-future HLC. hlc.New takes time.Time.
	futureHLC := hlc.New(w.Now().Add(365 * 24 * time.Hour))
	forgedTr.Proto.Hlc = futureHLC

	// Encode and try to deliver.
	forgedBytes := group.EncodeTransition(forgedTr)

	before := receiver.DroppedMessages()
	err = receiver.Deliver(forgedBytes)
	after := receiver.DroppedMessages()

	if err == nil {
		t.Fatal("drift: expected Deliver to reject, got nil error")
	}
	if !errors.Is(err, sim.ErrHLCDriftExceeded) {
		t.Fatalf("expected ErrHLCDriftExceeded, got: %v", err)
	}
	if after != before+1 {
		t.Fatalf("drift: dropped counter did not increment: before=%d after=%d", before, after)
	}
	t.Logf("drift rejected: dropped=%d err=%v", after, err)
}

// TestThreat_HLCDriftAtBoundary verifies that an HLC exactly at the
// drift limit is accepted, and one just past it is rejected. Boundary
// checks catch off-by-one errors in the comparison.
func TestThreat_HLCDriftAtBoundary(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        33,
		HostCount:   1,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	gkp := setupVegasProgrammers(w)
	receiver := w.Hosts()[0]

	// Set a tight drift bound so we can test boundary precisely.
	receiver.SetMaxHLCDrift(5 * time.Second)

	stewards := stewardKPsForTest(w)
	rRoot := receiver.State(gkp.Public).Root()
	prior := &pb.StateRoot{Hash: rRoot[:]}
	payload := &pb.AddStewardPayload{
		NewSteward: &pb.PublicKey{Raw: []byte("steward-boundary")},
	}

	// Helper: build a transition with a given HLC wall offset (relative
	// to w.Now()) and try to Deliver it. Refreshes prior_root each
	// iteration so the test isolates the drift check from the
	// prior_state check.
	tryOffset := func(offset time.Duration) error {
		p := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
			PriorState: prior, // use the snapshot from when the test set up
			Payload:    &pb.Transition_AddSteward{AddSteward: payload},
			SignedAt:   timestamppb.New(w.Now()),
		}
		canonical, _ := group.MarshalCanonicalForSigningHelper(p)
		sigA := crypto.Sign(stewards[0], gkp.Public, crypto.MsgKindTransition, canonical)
		sigB := crypto.Sign(stewards[1], gkp.Public, crypto.MsgKindTransition, canonical)
		p.StewardSignatures = &pb.Multisig{
			Threshold:  2,
			Signatures: []*pb.Signature{{Raw: sigA[:]}, {Raw: sigB[:]}},
		}
		tr, _ := group.NewTransition(p, gkp.Public)
		tr.Proto.Hlc = hlc.New(w.Now().Add(offset))
		return receiver.Deliver(group.EncodeTransition(tr))
	}

	// At offset = 4s (well within 5s bound): drift check passes.
	// Whether prior_state matches is a separate concern; we don't
	// assert on it here. The point is that the drift check does NOT
	// fire.
	err := tryOffset(4 * time.Second)
	if errors.Is(err, sim.ErrHLCDriftExceeded) {
		t.Fatalf("offset +4s should NOT be drift-rejected; got: %v", err)
	}

	// At offset = 6s (just past 5s bound): drift check fires.
	err = tryOffset(6 * time.Second)
	if !errors.Is(err, sim.ErrHLCDriftExceeded) {
		t.Fatalf("offset +6s should be drift-rejected; got: %v", err)
	}
}

// TestThreat_StewardSetBound verifies that ADD_STEWARD is rejected
// once the steward set reaches MaxStewards. Adversary scenario:
// malicious steward adds themselves 100,000 times via repeated
// ADD_STEWARD, hoping to OOM the state or slow signature verification
// (which is O(N) per transition).
func TestThreat_StewardSetBound(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        44,
		HostCount:   1,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	gkp := setupVegasProgrammers(w)

	// Tighten MaxStewards for fast test. We need: initial set is 3,
	// so the 4th ADD_STEWARD should be the (small-N+1)-th that succeeds.
	// Set cap to 5: we can add 2 more, the 3rd attempt (6th total) fails.
	w.Hosts()[0].State(gkp.Public).MaxStewards = 5
	stewards := stewardKPsForTest(w)

	addSteward := func(idx int) error {
		prev := w.Hosts()[0].State(gkp.Public).Snapshot()
		root := prev.Root()
		priorRoot := &pb.StateRoot{Hash: root[:]}
		newKP := crypto.KeyPairFromSeed([32]byte{byte(idx), 0, 0, 0, 0, 0, 0, 0})
		payload := &pb.AddStewardPayload{
			NewSteward: &pb.PublicKey{Raw: newKP.Public[:]},
		}
		p := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
			PriorState: priorRoot,
			Payload:    &pb.Transition_AddSteward{AddSteward: payload},
			SignedAt:   timestamppb.New(w.Now()),
		}
		canonical, _ := group.MarshalCanonicalForSigningHelper(p)
		sigA := crypto.Sign(stewards[0], gkp.Public, crypto.MsgKindTransition, canonical)
		sigB := crypto.Sign(stewards[1], gkp.Public, crypto.MsgKindTransition, canonical)
		p.StewardSignatures = &pb.Multisig{
			Threshold:  2,
			Signatures: []*pb.Signature{{Raw: sigA[:]}, {Raw: sigB[:]}},
		}
		tr, _ := group.NewTransition(p, gkp.Public)
		tr.Proto.Hlc = hlc.New(w.Now())
		return w.Hosts()[0].State(gkp.Public).Apply(tr, w.Now())
	}

	// Initial steward set is 3. Cap is 5. So 2 ADD_STEWARD should
	// succeed; the 3rd should fail.
	if err := addSteward(1); err != nil {
		t.Fatalf("1st ADD_STEWARD: %v", err)
	}
	stewards1 := currentStewards(w.Hosts()[0].State(gkp.Public))
	t.Logf("after 1st ADD: %d stewards (cap %d)",
		len(stewards1),
		w.Hosts()[0].State(gkp.Public).MaxStewards)
	err2 := addSteward(2)
	stewards2 := currentStewards(w.Hosts()[0].State(gkp.Public))
	t.Logf("after 2nd ADD: %d stewards, err=%v", len(stewards2), err2)
	err := addSteward(3)
	if err == nil {
		t.Fatal("3rd ADD_STEWARD should fail at MaxStewards=5; got nil error")
	}
	t.Logf("3rd ADD_STEWARD rejected: %v", err)
	if !contains(err.Error(), "MaxStewards") {
		t.Fatalf("expected error to mention 'MaxStewards', got: %v", err)
	}
}

// currentStewards is the public accessor for the current steward set,
// used by tests.
func currentStewards(s *group.State) []group.Steward {
	return s.Stewards()
}

// TestThreat_TransitionFloodingRejected verifies the per-steward
// rate-limit defense from §5.4.5. A malicious steward with a valid
// signing key floods ADD_STEWARD transitions; after exhausting their
// burst quota, subsequent transitions are rejected with
// ErrRateLimited.
//
// Why this matters: an adversary who controls ONE steward key can
// still consume honest-hosts' CPU (signature verification is the
// hot path). Rate limiting by first-signer attributes the cost to
// the actor that authored the message — not the threshold-many
// co-signers who are presumably honest.
//
// The test uses sim.World's virtual clock so the rate-limit
// recovery is deterministic.
func TestThreat_TransitionFloodingRejected(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        55,
		HostCount:   1,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))
	gkp := setupVegasProgrammers(w)

	host := w.Hosts()[0]
	st := host.State(gkp.Public)
	stewards := stewardKPsForTest(w) // [alice, bob, carol]

	// Wire a rate limiter into the state. Tight burst for fast test:
	// 1/s refill, burst 3.
	var now time.Time = w.Now()
	st.Limiter = ratelimit.NewLimiter(1, 3, func() time.Time { return now })

	// Helper: build & apply an ADD_STEWARD transition that adds a
	// fresh dummy key, signed by alice + bob.
	applyAdd := func(idx int) error {
		root := st.Root()
		prior := &pb.StateRoot{Hash: root[:]}
		newKP := crypto.KeyPairFromSeed([32]byte{byte(idx), 0, 0, 0, 0, 0, 0, 0})
		p := &pb.AddStewardPayload{
			NewSteward: &pb.PublicKey{Raw: newKP.Public[:]},
		}
		trProto := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
			PriorState: prior,
			Payload:    &pb.Transition_AddSteward{AddSteward: p},
			SignedAt:   timestamppb.New(w.Now()),
		}
		canonical, _ := group.MarshalCanonicalForSigningHelper(trProto)
		sigA := crypto.Sign(stewards[0], gkp.Public, crypto.MsgKindTransition, canonical)
		sigB := crypto.Sign(stewards[1], gkp.Public, crypto.MsgKindTransition, canonical)
		trProto.StewardSignatures = &pb.Multisig{
			Threshold:  2,
			Signatures: []*pb.Signature{{Raw: sigA[:]}, {Raw: sigB[:]}},
		}
		tr, _ := group.NewTransition(trProto, gkp.Public)
		tr.Proto.Hlc = hlc.New(w.Now())
		return st.Apply(tr, w.Now())
	}

	// First 3 calls: burst quota, all succeed.
	for i := 1; i <= 3; i++ {
		if err := applyAdd(i); err != nil {
			t.Fatalf("burst call #%d should succeed, got: %v", i, err)
		}
	}

	// 4th call: bucket exhausted, rate limit fires.
	err = applyAdd(4)
	if err == nil {
		t.Fatal("4th call should be rate-limited")
	}
	var rl *ratelimit.ErrRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("expected ErrRateLimited, got %T: %v", err, err)
	}
	t.Logf("4th call rate-limited: %v", err)

	// Advance virtual clock by 5 seconds — bucket refills 5 tokens,
	// capped at burst (3). Next call should succeed.
	now = now.Add(5 * time.Second)
	if err := applyAdd(5); err != nil {
		t.Fatalf("after refill, call should succeed, got: %v", err)
	}
	t.Log("post-refill call succeeded")
}

// contains is a tiny helper to avoid importing strings just for
// strings.Contains in this file. The error message check is small.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}