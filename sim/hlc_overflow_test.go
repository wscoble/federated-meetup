// SPDX-License-Identifier: MIT
//
// Cycle 42: HLC counter overflow boundary.
//
// The HLC (internal/hlc/hlc.go) uses uint16 for the logical counter.
// On overflow (counter=0xFFFF), Tick and Observe bump the wall
// component by 1 nanosecond and reset the counter to 0 — per the
// Kulkarni et al. HLC paper prescription.
//
// This test exercises that boundary to confirm:
//
//   1. Tick at counter=0xFFFF → counter resets to 0, wall advances
//   2. Observe at counter=0xFFFF → counter resets to 0, wall advances
//   3. After overflow, HLC ordering is still monotonic (later > earlier)
//   4. The wall-component bump is exactly 1 nanosecond
//
// Why this matters: counter overflow is rare (65535 events in one
// nanosecond) but the protocol must handle it gracefully. A naive
// implementation that wraps to 0 without bumping the wall would
// produce an HLC that compares LESS than the previous one — breaking
// the monotonicity invariant.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/hlc"
)

func TestHLC_CounterOverflow_Tick(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	// Construct an HLC with counter at 0xFFFF (max uint16).
	craft := make([]byte, hlc.Size)
	// 16-byte wall nanos (BigEndian uint64) — use 'now'
	ns := uint64(now.UnixNano())
	craft[0] = byte(ns >> 56)
	craft[1] = byte(ns >> 48)
	craft[2] = byte(ns >> 40)
	craft[3] = byte(ns >> 32)
	craft[4] = byte(ns >> 24)
	craft[5] = byte(ns >> 16)
	craft[6] = byte(ns >> 8)
	craft[7] = byte(ns)
	// 2-byte counter = 0xFFFF
	craft[16] = 0xFF
	craft[17] = 0xFF

	priorFull, err := hlc.FromProto(craft)
	if err != nil {
		t.Fatal(err)
	}
	if priorFull.Counter() != 0xFFFF {
		t.Fatalf("crafted HLC counter = %d, want 65535", priorFull.Counter())
	}

	// Tick at the same wall time → counter overflows → wall advances.
	next, err := hlc.Tick(priorFull, now)
	if err != nil {
		t.Fatal(err)
	}

	// After overflow: wall is now+1ns, counter is 0.
	wantNanos := uint64(now.UnixNano()) + 1
	gotNanos := uint64(next.Time().UnixNano())
	if gotNanos != wantNanos {
		t.Errorf("post-overflow wall = %d, want %d (now+1ns)", gotNanos, wantNanos)
	}
	if next.Counter() != 0 {
		t.Errorf("post-overflow counter = %d, want 0", next.Counter())
	}

	// Monotonicity: next > priorFull.
	if !next.After(priorFull) {
		t.Errorf("post-overflow HLC not after pre-overflow: next=%s prior=%s", next, priorFull)
	}
	t.Logf("counter overflow handled: wall %d → %d, counter 65535 → 0", now.UnixNano(), gotNanos)
}

func TestHLC_CounterOverflow_Observe(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	craft := make([]byte, hlc.Size)
	ns := uint64(now.UnixNano())
	craft[0] = byte(ns >> 56)
	craft[1] = byte(ns >> 48)
	craft[2] = byte(ns >> 40)
	craft[3] = byte(ns >> 32)
	craft[4] = byte(ns >> 24)
	craft[5] = byte(ns >> 16)
	craft[6] = byte(ns >> 8)
	craft[7] = byte(ns)
	craft[16] = 0xFF
	craft[17] = 0xFF
	remoteFull, err := hlc.FromProto(craft)
	if err != nil {
		t.Fatal(err)
	}

	// Local cursor also at counter=0xFFFF, same wall.
	localFull, err := hlc.FromProto(craft)
	if err != nil {
		t.Fatal(err)
	}

	// Observe: max wall == now, both last/remote at counter=0xFFFF.
	// Per Observe logic (lines 224-244): counter is max+1, but counter
	// is 0xFFFF → overflow → wall bumps to now+1ns, counter resets to 0.
	next, err := hlc.Observe(localFull, remoteFull, now)
	if err != nil {
		t.Fatal(err)
	}

	wantNanos := uint64(now.UnixNano()) + 1
	gotNanos := uint64(next.Time().UnixNano())
	if gotNanos != wantNanos {
		t.Errorf("post-overflow Observe wall = %d, want %d", gotNanos, wantNanos)
	}
	if next.Counter() != 0 {
		t.Errorf("post-overflow Observe counter = %d, want 0", next.Counter())
	}
	t.Logf("Observe counter overflow handled: wall bumped 1ns, counter reset")
}