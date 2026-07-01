// SPDX-License-Identifier: MIT
//
// Audit C-3 (cycle 51): equivocation-log eviction observability.
//
// The eviction counter (State.EquivocationEvictions) and the structured
// warning log give operators and honest peers a way to detect that the
// equivocation detection window has shrunk. These two tests formalize:
//
//   1. Observability contract — two honest hosts that see the same
//      sequence of transitions agree on the eviction count.
//   2. Security trade-off — after eviction, a forked transition at the
//      evicted (steward, prior_state) pair is no longer detectable as
//      equivocation. This is the known, documented trade-off; the test
//      asserts it so future changes don't silently "fix" it by accident
//      (which would change the memory-bound semantics).

package sim_test

import (
	"testing"

	"github.com/sscoble/federated-meetup/internal/group"
)

// TestC3_EvictionObservabilityContract asserts that two hosts
// processing the same sequence of transitions observe the same
// eviction count. If they diverge, something is non-deterministic
// about the eviction path.
func TestC3_EvictionObservabilityContract(t *testing.T) {
	// Two independent logs with the same small cap.
	cap := 5
	logA := group.NewEquivocationLogForTest(cap)
	logB := group.NewEquivocationLogForTest(cap)

	// Insert cap+10 entries with unique (steward, prior) keys.
	for i := 0; i < cap+10; i++ {
		var steward [32]byte
		var prior [32]byte
		steward[0] = byte(i)
		prior[0] = byte(i + 1)
		hlc := []byte{byte(i), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		var txhash [32]byte
		txhash[0] = byte(i)
		logA.InsertForTest(steward, prior, hlc, txhash)
		logB.InsertForTest(steward, prior, hlc, txhash)
	}

	// Both must agree on eviction count.
	// We inserted cap+10 = 15 entries into a cap-5 log, so 10 evictions.
	gotA := logA.Evictions()
	gotB := logB.Evictions()

	if gotA != gotB {
		t.Errorf("observability contract violated: host A evictions=%d, host B evictions=%d", gotA, gotB)
	}
	if gotA != 10 {
		t.Errorf("expected 10 evictions (15 inserts - cap 5), got %d", gotA)
	}
	t.Logf("observability contract OK: both hosts agree on %d evictions", gotA)
}

// TestC3_SecurityTradeOffAfterEviction asserts that after an entry
// is evicted, a conflicting transition at the same (steward, prior)
// pair is NOT detected as equivocation. This is the documented
// security trade-off: the FIFO eviction silently disables detection
// for evicted pairs. The test pins this behavior so it can't change
// accidentally without a deliberate code review.
func TestC3_SecurityTradeOffAfterEviction(t *testing.T) {
	cap := 3
	eqLog := group.NewEquivocationLogForTest(cap)

	// Insert entry for steward A at prior_state P1.
	var stewardA [32]byte
	stewardA[0] = 0xAA
	var priorP1 [32]byte
	priorP1[0] = 0x01
	hlc1 := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	var txhash1 [32]byte
	txhash1[0] = 0x11
	eqLog.InsertForTest(stewardA, priorP1, hlc1, txhash1)

	// Fill the log past cap to evict entry for (stewardA, priorP1).
	for i := 0; i < cap; i++ {
		var s [32]byte
		var p [32]byte
		s[0] = byte(0xBB + i)
		p[0] = byte(0x02 + i)
		hlc := []byte{byte(2 + i), 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
		var tx [32]byte
		tx[0] = byte(0x22 + i)
		eqLog.InsertForTest(s, p, hlc, tx)
	}

	// (stewardA, priorP1) should have been evicted.
	if eqLog.HasForTest(stewardA, priorP1) {
		t.Fatal("expected (stewardA, priorP1) to be evicted, but it's still in the log")
	}
	if eqLog.Evictions() == 0 {
		t.Fatal("expected non-zero eviction count, got 0")
	}

	// Now try to detect equivocation at the evicted pair.
	// Use a different HLC and txhash (would be equivocation if the
	// entry were still present).
	hlc2 := []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	var txhash2 [32]byte
	txhash2[0] = 0x22
	detected := eqLog.CheckForTest(stewardA, priorP1, hlc2, txhash2)

	if detected {
		t.Error("SECURITY TRADE-OFF BROKEN: equivocation detected after eviction — " +
			"either the log is not evicting or detection is running outside the window. " +
			"This test pins the documented behavior: evicted pairs are undetectable.")
	}
	t.Logf("security trade-off confirmed: after eviction, equivocation at "+
		"(steward=%x, prior=%x) is NOT detected (evictions=%d)",
		stewardA[:8], priorP1[:8], eqLog.Evictions())
}