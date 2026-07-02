// SPDX-License-Identifier: MIT
//
// Rate-limit-before-equivocation invariant. The apply path in
// internal/group/group.go charges the signing steward's token
// bucket BEFORE checking the equivocation log. This protects
// honest hosts from a malicious steward polluting the equivocation
// log with phantom entries via rate-limited attempts.
//
// What this exercises:
//   - After the rate limiter rejects a transition, the equivocation
//     log does NOT contain an entry for the rejected (steward, prior)
//     pair.
//   - A subsequent legitimate transition at the SAME prior (or a
//     different prior) from the same steward is not spuriously
//     flagged as equivocation.
//
// Why this matters: the rate limiter rejects with a 429-equivalent
// error and returns early. If the equivocation log were updated
// before the rate check, a flood of bad attempts would poison the
// log and prevent legitimate future transitions. The ordering
// matters for correctness.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/hlc"
	"github.com/sscoble/federated-meetup/internal/ratelimit"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRateLimit_DoesNotPolluteEquivocationLog(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        87,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)
	h0 := w.Hosts()[0]
	st := h0.State(gkp.Public)

	// Wire a tight rate limiter: 1 token/s refill, burst 3.
	var now time.Time = w.Now()
	st.Limiter = ratelimit.NewLimiter(1, 3, func() time.Time { return now })

	// Helper: build & apply a fresh ADD_STEWARD at the current
	// root, advancing to a new root each call.
	applyAdd := func(idx int) error {
		root := st.Root()
		newKP := crypto.KeyPairFromSeed([32]byte{byte(idx), 0, 0, 0, 0, 0, 0, 0})
		p := &pb.AddStewardPayload{NewSteward: &pb.PublicKey{Raw: newKP.Public[:]}}
		trProto := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
			PriorState: &pb.StateRoot{Hash: root[:]},
			Payload:    &pb.Transition_AddSteward{AddSteward: p},
			SignedAt:   timestamppb.New(w.Now()),
		}
		canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
		if err != nil {
			return err
		}
		sigA := crypto.Sign(stewards[0], gkp.Public, crypto.MsgKindTransition, canonical)
		sigB := crypto.Sign(stewards[1], gkp.Public, crypto.MsgKindTransition, canonical)
		trProto.StewardSignatures = &pb.Multisig{
			Threshold:  2,
			Signatures: []*pb.Signature{{Raw: sigA[:]}, {Raw: sigB[:]}},
		}
		tr, err := group.NewTransition(trProto, gkp.Public)
		if err != nil {
			return err
		}
		tr.Proto.Hlc = hlc.New(w.Now())
		return st.Apply(tr, w.Now())
	}

	// Drain the bucket with 3 successful adds.
	for i := 1; i <= 3; i++ {
		if err := applyAdd(i); err != nil {
			t.Fatalf("burst call #%d should succeed, got: %v", i, err)
		}
	}

	// Bucket is empty. 4th call must be rate-limited.
	rootBeforeExhaust := st.Root()
	if err := applyAdd(4); err == nil {
		t.Fatal("4th call should be rate-limited")
	} else {
		t.Logf("4th call rate-limited (expected): %v", err)
	}
	if st.Root() != rootBeforeExhaust {
		t.Fatalf("state root advanced despite rate-limit rejection")
	}

	// Advance the clock past the refill window. Bucket gets 1
	// token back.
	now = now.Add(2 * time.Second)

	// A fresh transition at the SAME root should now succeed —
	// proves the rate-limited attempt did NOT pollute the
	// equivocation log for (alice, rootBeforeExhaust).
	if err := applyAdd(5); err != nil {
		t.Fatalf("post-refill add failed (equivocation log may have been polluted by rate-limited attempt): %v", err)
	}
	if st.Root() == rootBeforeExhaust {
		t.Fatal("post-refill add did not advance root")
	}
	t.Logf("post-refill add succeeded; equivocation log clean after rate-limit rejection")
}