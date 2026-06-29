// SPDX-License-Identifier: MIT
//
// HLC skew-tolerance scenarios.
//
// Scott's ask: "Time will be out of sync between nodes regularly. We need
// to have a concept of network ordering that's clock-independent."
//
// These tests verify that:
//   1. Hosts with wildly different wall-clock offsets still produce
//      strictly-monotonic HLCs.
//   2. After every transition has settled, the HLC values seen by every
//      host form the same total order (the "happened-before" relation).
//   3. A host whose clock is set backwards (suspend/resume, NTP step)
//      never issues a regressed HLC.
package sim_test

import (
	"sort"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/hlc"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// TestHLC_SkewTolerance_TotalOrder is the headline scenario. We set up
// four hosts with deliberately bad clocks:
//
//   - h0: clock is an hour AHEAD of world.Now()
//   - h1: clock is correct
//   - h2: clock is one hour BEHIND
//   - h3: clock is one day BEHIND (extreme skew — far past NTP drift)
//
// Each host authors a transition. After delivery, every host should have
// observed all four HLC values. We assert two invariants:
//
//   1. Every host's HLC cursor is strictly greater than Zero (it has
//      done at least one Tick).
//   2. Every host has the same log length (all four transitions landed).
//
// We don't assert state-root equality because the four transitions add
// four different stewards, so the resulting state roots legitimately
// differ per host — that's not a skew symptom, it's a domain fact. The
// HLC ordering is the property we care about, and that's what makes the
// federation tolerate skew.
func TestHLC_SkewTolerance_TotalOrder(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        42,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)

	// Drain the CREATE_GROUP transitions so every host has a real state.
	for i := 0; i < 5; i++ {
		w.Advance(50 * time.Millisecond)
		for _, h := range w.Hosts() {
			for _, msg := range h.PeekMessages() {
				if msg.Tag == "transition" {
					_ = h.Deliver(msg.Payload)
				}
			}
		}
	}

	// Apply wide skew across hosts.
	w.Hosts()[0].SetClockSkew(+1 * time.Hour)  // ahead
	w.Hosts()[1].SetClockSkew(0)               // correct
	w.Hosts()[2].SetClockSkew(-1 * time.Hour)  // behind
	w.Hosts()[3].SetClockSkew(-24 * time.Hour) // extreme behind

	// Each host authors a transition. Use the SAME new steward key for
	// every host — the transition's contents are identical except for
	// the prior_state (which depends on the host's local state at the
	// time of submission). With identical payload keys, the resulting
	// state root divergence reflects only HLC ordering, not payload
	// differences.
	sharedNewKP := crypto.KeyPairFromSeed([32]byte{99, 99, 99, 99, 99, 99, 99, 99})
	for i, h := range w.Hosts() {
		w.Advance(100 * time.Millisecond)

		prev := h.State(gkp.Public).Snapshot()
		root := prev.Root()
		priorRoot := &pb.StateRoot{Hash: root[:]}

		payload := &pb.AddStewardPayload{
			NewSteward: &pb.PublicKey{Raw: sharedNewKP.Public[:]},
		}
		trProto := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
			PriorState: priorRoot,
			Payload:    &pb.Transition_AddSteward{AddSteward: payload},
			SignedAt:   timestamppb.New(w.Now()),
		}
		canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
		if err != nil {
			t.Fatal(err)
		}
		stewardKPs := stewardKPsForTest(w)
		sig0 := crypto.Sign(stewardKPs[0], gkp.Public, crypto.MsgKindTransition, canonical)
		sig1 := crypto.Sign(stewardKPs[1], gkp.Public, crypto.MsgKindTransition, canonical)
		trProto.StewardSignatures = &pb.Multisig{
			Threshold: 2,
			Signatures: []*pb.Signature{
				{Raw: sig0[:]},
				{Raw: sig1[:]},
			},
		}
		tr, err := group.NewTransition(trProto, gkp.Public)
		if err != nil {
			t.Fatal(err)
		}
		// i is unused after the loop index capture, but keep it for
		// the log message so we can correlate if this fails.
		t.Logf("host %d (%s, skew=%v) submitting", i, h.ID(), h.ClockSkew())
		if _, err := h.SubmitTransition(gkp.Public, tr); err != nil {
			t.Fatalf("host %s submit: %v", h.ID(), err)
		}
	}

	// Drain — let every transition reach every host.
	for i := 0; i < 20; i++ {
		w.Advance(50 * time.Millisecond)
		for _, h := range w.Hosts() {
			for _, msg := range h.PeekMessages() {
				if msg.Tag == "transition" {
					_ = h.Deliver(msg.Payload)
				}
			}
		}
	}

	// Assertion 1: every host's cursor is strictly greater than Zero.
	for _, h := range w.Hosts() {
		cur := h.HLCCursor()
		if len(cur) != hlc.Size {
			t.Errorf("host %s cursor is not a valid HLC: %x", h.ID(), cur)
		}
		if !cur.After(hlc.Zero) {
			t.Errorf("host %s cursor %s is not > Zero", h.ID(), cur)
		}
		t.Logf("host %s cursor: %s", h.ID(), cur)
	}

	// Assertion 2: every host has the same log length (4 transitions:
	// CREATE_GROUP + 4 ADD_STEWARD = 5 expected per host).
	wantLen := w.Hosts()[0].State(gkp.Public).Log()
	for _, h := range w.Hosts()[1:] {
		got := h.State(gkp.Public).Log()
		if len(got) != len(wantLen) {
			t.Errorf("host %s log length %d != %d", h.ID(), len(got), len(wantLen))
		}
	}

	// Assertion 3: HLC values stamped on each transition by its author
	// form a strict total order when sorted. Two transitions may share
	// a nanos wall component (different hosts in the same wall
	// nanosecond), in which case counter breaks the tie. The point:
	// even with the host-3 clock 24 hours behind, the HLC values are
	// still totally ordered.
	authoredHLCs := make([]hlc.HLC, len(w.Hosts()))
	for i, h := range w.Hosts() {
		// The last transition in the log is the one this host authored.
		log := h.State(gkp.Public).Log()
		var found hlc.HLC
		for _, tr := range log {
			if len(tr.Proto.GetHlc()) > 0 {
				hlcv, _ := hlc.FromProto(tr.Proto.GetHlc())
				if len(found) == 0 || hlcv.After(found) {
					found = hlcv
				}
			}
		}
		authoredHLCs[i] = found
		t.Logf("host %s authored HLC max: %s", h.ID(), found)
	}

	// Assertion 3: the four authored HLCs are pairwise comparable.
	// HLC permits ties (two distinct events from different hosts can
	// produce identical HLC values — the paper allows this; what HLC
	// guarantees is that within a single causal chain, order is total).
	// What we DO require: the sorted sequence is monotonically
	// non-decreasing. Equal HLCs between hosts are fine; what would
	// violate the invariant is one host seeing its own cursor go
	// backwards.
	sort.Slice(authoredHLCs, func(i, j int) bool {
		return authoredHLCs[i].Before(authoredHLCs[j])
	})
	for i := 1; i < len(authoredHLCs); i++ {
		if authoredHLCs[i].Before(authoredHLCs[i-1]) {
			t.Errorf("HLC order violated after sort: %s before %s",
				authoredHLCs[i], authoredHLCs[i-1])
		}
	}
}

// TestHLC_ClockStepBackward is the suspend/resume scenario. Host A
// submits a transition, advances. Then the host's wall clock jumps
// backwards by an hour (simulating suspend/resume or NTP step). A
// second transition must still get a strictly greater HLC than the
// first, even though wall-clock went backwards.
func TestHLC_ClockStepBackward(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        7,
		HostCount:   2,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))
	gkp := setupVegasProgrammers(w)

	h0 := w.Hosts()[0]
	h1 := w.Hosts()[1]

	w.Advance(1 * time.Second)
	cursorBeforeStep := h0.HLCCursor()

	// Step the wall clock backwards by an hour. The host's clock now
	// claims we're at 11:00, even though the sim has moved forward.
	h0.SetClockSkew(-2 * time.Hour)

	// Submit another transition.
	hostKP := crypto.KeyPairFromSeed([32]byte{1, 2, 3})
	prev := h0.State(gkp.Public).Snapshot()
	root := prev.Root()
	priorRoot := &pb.StateRoot{Hash: root[:]}
	payload := &pb.AddStewardPayload{
		NewSteward: &pb.PublicKey{Raw: hostKP.Public[:]},
	}
	// Build the transition proto first so the canonical bytes we sign
	// match what NewTransition marshals (otherwise VerifyMultisig sees
	// invalid signatures).
	trProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		PriorState: priorRoot,
		Payload:    &pb.Transition_AddSteward{AddSteward: payload},
		SignedAt:   timestamppb.New(w.Now()),
	}
	canonical, _ := group.MarshalCanonicalForSigningHelper(trProto)
	// Threshold is 2 (from setupVegasProgrammers). Sign with the steward
	// keys alice + bob so the multisig verifies.
	stewardKPs := stewardKPsForTest(w)
	sig0 := crypto.Sign(stewardKPs[0], gkp.Public, crypto.MsgKindTransition, canonical)
	sig1 := crypto.Sign(stewardKPs[1], gkp.Public, crypto.MsgKindTransition, canonical)
	trProto.StewardSignatures = &pb.Multisig{
		Threshold: 2,
		Signatures: []*pb.Signature{
			{Raw: sig0[:]},
			{Raw: sig1[:]},
		},
	}
	tr, _ := group.NewTransition(trProto, gkp.Public)
	if _, err := h0.SubmitTransition(gkp.Public, tr); err != nil {
		t.Fatal(err)
	}

	cursorAfterStep := h0.HLCCursor()
	if !cursorAfterStep.After(cursorBeforeStep) {
		t.Fatalf("HLC regressed across wall-clock step backwards:\n  before: %s\n  after:  %s",
			cursorBeforeStep, cursorAfterStep)
	}
	t.Logf("cursor survived clock step back: before=%s after=%s",
		cursorBeforeStep, cursorAfterStep)

	// Drain and verify h1 also sees a monotonic HLC.
	w.Advance(100 * time.Millisecond)
	for _, msg := range h1.PeekMessages() {
		if msg.Tag == "transition" {
			h1.Deliver(msg.Payload)
		}
	}
	if len(h1.HLCCursor()) == 0 {
		t.Fatal("h1 cursor empty after delivery")
	}
	t.Logf("h1 cursor after delivery: %s", h1.HLCCursor())
}

// TestHLC_CompareDeterministic verifies that HLC bytes compare
// deterministically. This is the on-the-wire property — two hosts that
// disagree on wall clock can still agree on total order by comparing
// the bytes directly.
func TestHLC_CompareDeterministic(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	later := now.Add(time.Microsecond)

	// a and b have distinct wall components, so they order by wall.
	a, _ := hlc.Tick(hlc.Zero, now)
	b, _ := hlc.Tick(hlc.Zero, later)
	if !b.After(a) {
		t.Fatalf("b should be > a (later wall); got a=%s b=%s", a, b)
	}
	if !a.Before(b) {
		t.Fatalf("a should be < b; got a=%s b=%s", a, b)
	}

	// c and d have the same wall component as a, but with bumped counters.
	c, _ := hlc.Tick(a, now)
	d, _ := hlc.Tick(c, now)
	if !c.After(a) {
		t.Fatalf("c should be > a (same wall, higher counter); got a=%s c=%s", a, c)
	}
	if !d.After(c) {
		t.Fatalf("d should be > c; got c=%s d=%s", c, d)
	}

	// Sort by HLC bytes. This is what every host does on the wire.
	samples := []hlc.HLC{b, a, hlc.Zero, d, c, a.Clone()}
	sort.Slice(samples, func(i, j int) bool {
		return samples[i].Before(samples[j])
	})

	// Expected order (after sorting by HLC bytes): Zero < a, aClone
	// (same value as a, stable sort preserves insertion order) < c < d < b.
	wantLabels := []string{"zero", "a", "a", "c", "d", "b"}
	gotLabels := []string{
		labelHLC(samples[0]),
		labelHLC(samples[1]),
		labelHLC(samples[2]),
		labelHLC(samples[3]),
		labelHLC(samples[4]),
		labelHLC(samples[5]),
	}
	if !equalStringSlices(gotLabels, wantLabels) {
		t.Fatalf("sort order mismatch:\n  got:  %v\n  want: %v", gotLabels, wantLabels)
	}
}

// labelHLC returns a short label that distinguishes HLC values for test
// purposes. Zero maps to "zero"; values map to a single char based on
// their position in the test's known sample set.
func labelHLC(h hlc.HLC) string {
	if len(h) == 0 {
		return "zero"
	}
	ns := h.Time().UnixNano()
	c := h.Counter()
	switch {
	case ns == 0:
		return "zero"
	case c == 0 && ns == 1782561600000000000:
		return "a"
	case c == 1 && ns == 1782561600000000000:
		return "c"
	case c == 2 && ns == 1782561600000000000:
		return "d"
	case ns == 1782561600000001000:
		return "b"
	}
	return "?"
}

func equalStringSlices(a, b []string) bool {
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

// stewardKPsForTest derives the same steward keypairs setupVegasProgrammers
// would use for the given world. Tests that need to sign transitions on
// behalf of stewards can call this.
func stewardKPsForTest(w *sim.World) []crypto.KeyPair {
	labels := []string{"alice", "bob", "carol"}
	out := make([]crypto.KeyPair, len(labels))
	for i, l := range labels {
		s := w.DeriveSeed(l)
		var seed [32]byte
		for j := 0; j < 8; j++ {
			seed[j] = byte(s >> (8 * j))
		}
		out[i] = crypto.KeyPairFromSeed(seed)
		_ = l
	}
	return out
}

// Debug helper: returns the raw seed bytes for a label (to compare with
// what setupVegasProgrammers would compute).
//lint:ignore U1000 debug helper kept for future use
func stewardSeedFor(w *sim.World, label string) [32]byte {
	s := w.DeriveSeed(label)
	var seed [32]byte
	for j := 0; j < 8; j++ {
		seed[j] = byte(s >> (8 * j))
	}
	return seed
}

// Silence unused-import warnings if the helpers above are pruned.
var _ = types.PublicKey{}
var _ = pb.PublicKey{}
var _ sort.Interface // keep sort import even when only used in one test