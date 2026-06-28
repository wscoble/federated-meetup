// SPDX-License-Identifier: MIT
//
// HLC unit tests. Pure, no simulator dependencies — these verify the
// ordering invariants directly.

package hlc_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/hlc"
)

// TestHLC_Tick_Advances verifies the basic monotonic property: Tick on
// a fresh prior returns a value > Zero and >= the input time.
func TestHLC_Tick_Advances(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	h, err := hlc.Tick(hlc.Zero, now)
	if err != nil {
		t.Fatal(err)
	}
	if !h.After(hlc.Zero) {
		t.Fatalf("Tick from Zero must produce h > Zero; got %s", h)
	}
	if h.Time() != now {
		t.Fatalf("Tick wall component: got %v want %v", h.Time(), now)
	}
	if h.Counter() != 0 {
		t.Fatalf("first tick should have counter 0; got %d", h.Counter())
	}
}

// TestHLC_Tick_Monotonic verifies that successive Ticks are strictly
// monotonic even when the wall clock doesn't advance.
func TestHLC_Tick_Monotonic(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	h1, _ := hlc.Tick(hlc.Zero, now)
	h2, _ := hlc.Tick(h1, now)
	h3, _ := hlc.Tick(h2, now)

	if !h2.After(h1) {
		t.Fatalf("h2 should be > h1; got h1=%s h2=%s", h1, h2)
	}
	if !h3.After(h2) {
		t.Fatalf("h3 should be > h2; got h2=%s h3=%s", h2, h3)
	}
	// Counter should increment when wall doesn't advance.
	if h2.Counter() != 1 || h3.Counter() != 2 {
		t.Fatalf("counter should tick up under frozen wall: h1.c=%d h2.c=%d h3.c=%d",
			h1.Counter(), h2.Counter(), h3.Counter())
	}
}

// TestHLC_Observe_AdvancesPastRemote verifies that observing a remote
// HLC with a higher wall component bumps the local HLC past it.
func TestHLC_Observe_AdvancesPastRemote(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	later := now.Add(1 * time.Hour)

	local, _ := hlc.Tick(hlc.Zero, now)
	remote, _ := hlc.Tick(hlc.Zero, later)

	out, err := hlc.Observe(local, remote, now)
	if err != nil {
		t.Fatal(err)
	}
	if !out.After(remote) {
		t.Fatalf("after Observe, local hlc must be > remote; got %s, remote=%s", out, remote)
	}
	if !out.After(local) {
		t.Fatalf("after Observe, local hlc must be > prior local; got %s, local=%s", out, local)
	}
}

// TestHLC_Observe_RespectsLocalWhenAhead verifies that when local wall
// is already ahead of remote, Observe doesn't regress.
func TestHLC_Observe_RespectsLocalWhenAhead(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	earlier := now.Add(-1 * time.Hour)

	local, _ := hlc.Tick(hlc.Zero, now)
	remote, _ := hlc.Tick(hlc.Zero, earlier)

	out, err := hlc.Observe(local, remote, now)
	if err != nil {
		t.Fatal(err)
	}
	// Local was already ahead; Observe should bump past local, not
	// regress toward remote.
	if !out.After(local) {
		t.Fatalf("out must be > local; got %s, local=%s", out, local)
	}
}

// TestHLC_Observe_TotalOrderAcrossHosts is the property Scott asked for:
// two hosts with very different wall-clocks produce a total order via
// HLC. We simulate the classic case where host A's clock is an hour
// behind host B's, and both exchange messages. The HLC values, when
// sorted, give a consistent view across both hosts.
func TestHLC_Observe_TotalOrderAcrossHosts(t *testing.T) {
	hostAClock := time.Date(2026, 6, 27, 11, 0, 0, 0, time.UTC) // an hour behind
	hostBClock := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC) // correct time

	// Host A generates an event at 11:00.
	a1, _ := hlc.Tick(hlc.Zero, hostAClock)

	// Host A's event reaches Host B at 12:00. Host B observes.
	bAfterA, _ := hlc.Observe(hlc.Zero, a1, hostBClock)

	// Host B then generates an event at 12:00:01.
	b1, _ := hlc.Tick(bAfterA, hostBClock.Add(time.Second))

	// Host B's event reaches Host A. A observes (even though A's wall
	// clock still says 11:00:05 or whatever).
	aAfterB, _ := hlc.Observe(a1, b1, hostAClock.Add(5*time.Second))

	// The total order must be: a1 < b1 < aAfterB, even though wall clocks
	// would suggest b1 came before a1 chronologically.
	if !a1.Before(b1) {
		t.Fatalf("a1 should precede b1 causally; got a1=%s b1=%s", a1, b1)
	}
	if !b1.Before(aAfterB) {
		t.Fatalf("b1 should precede aAfterB causally; got b1=%s aAfterB=%s", b1, aAfterB)
	}
}

// TestHLC_ClockGoesBackwards verifies that Tick handles a wall clock
// that moved backwards (NTP step, suspend/resume). The HLC sticks with
// the most recent seen value and increments counter.
func TestHLC_ClockGoesBackwards(t *testing.T) {
	t1 := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	t0 := t1.Add(-1 * time.Hour)

	h1, _ := hlc.Tick(hlc.Zero, t1)
	h2, _ := hlc.Tick(h1, t0) // clock went BACK

	if !h2.After(h1) {
		t.Fatalf("h2 must be > h1 even when wall goes back; got h1=%s h2=%s", h1, h2)
	}
	if h2.Time() != h1.Time() {
		t.Fatalf("wall component should stick; got h2.Time=%v h1.Time=%v", h2.Time(), h1.Time())
	}
	if h2.Counter() != h1.Counter()+1 {
		t.Fatalf("counter should bump; got h1.c=%d h2.c=%d", h1.Counter(), h2.Counter())
	}
}

// TestHLC_CounterOverflow handles the rare overflow case.
func TestHLC_CounterOverflow(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)

	// Build an HLC with counter at 0xFFFE.
	last := hlc.New(now)
	bumped := append([]byte(nil), last...)
	bumped[16] = 0xFF
	bumped[17] = 0xFE

	h, err := hlc.Tick(bumped, now)
	if err != nil {
		t.Fatal(err)
	}
	if !h.After(bumped) {
		t.Fatalf("h must be > bumped; got h=%s bumped=%s", h, bumped)
	}
	if h.Counter() != 0xFFFF {
		t.Fatalf("h.counter should be 0xFFFF; got %d", h.Counter())
	}

	// Next Tick at same wall component — counter overflow. Should bump
	// wall by 1ns and reset counter.
	h2, _ := hlc.Tick(h, now)
	if h2.Counter() != 0 {
		t.Fatalf("overflow: counter should reset to 0; got %d", h2.Counter())
	}
	if h2.Time() != now.Add(time.Nanosecond) {
		t.Fatalf("overflow: wall should bump by 1ns; got %v want %v", h2.Time(), now.Add(time.Nanosecond))
	}
}

// TestHLC_RoundTrip verifies FromProto + Size invariance.
func TestHLC_RoundTrip(t *testing.T) {
	now := time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)
	h, _ := hlc.Tick(hlc.Zero, now)
	if len(h) != hlc.Size {
		t.Fatalf("HLC should be %d bytes; got %d", hlc.Size, len(h))
	}
	restored, err := hlc.FromProto(h)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(restored, h) {
		t.Fatalf("round-trip mismatch: %x vs %x", restored, h)
	}
}

// TestHLC_BadSize verifies FromProto rejects malformed input.
func TestHLC_BadSize(t *testing.T) {
	if _, err := hlc.FromProto([]byte{1, 2, 3}); err == nil {
		t.Fatal("expected error on bad size")
	}
}