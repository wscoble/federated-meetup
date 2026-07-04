// SPDX-License-Identifier: AGPL-3.0
//
// Cycle 41: equivocation log eviction behavior.
//
// The equivocation log (internal/group/equivocation.go:59) uses FIFO
// eviction bounded by maxEntries (default?). Once the log is full,
// the oldest entry is dropped. A dropped entry means that a future
// conflicting transition at the same (steward, prior_state) would be
// mis-classified as "first time" — no equivocation detected.
//
// What this pins down:
//
//   1. The log has a finite capacity (default 0 = unbounded? or 10000?)
//   2. When full, FIFO eviction removes the OLDEST entry
//   3. After eviction, the next entry at that (steward, prior_state)
//      pair is treated as fresh — no conflict detected
//
// Why this matters: equivocation detection is the G3 defense against
// malicious stewards. If the log is bounded and forgets old entries,
// a long-running group could see its detection window close over.
// This isn't a bug per se (it's an explicit memory/DoS tradeoff) but
// the SPEC should formalize the eviction policy so hosts agree on
// which evictions are observable.
package sim_test

import (
	"testing"

	"github.com/wscoble/federated-meetup/internal/group"
)

func TestEquivocationLog_EvictionPolicyIsFIFO(t *testing.T) {
	// Build a log with explicit small capacity.
	log := group.NewEquivocationLogForTest(3)

	// Insert 5 distinct (steward, prior) pairs.
	log.InsertForTest([32]byte{1}, [32]byte{0xAA}, []byte("hlc-1"), [32]byte{0x01})
	log.InsertForTest([32]byte{2}, [32]byte{0xAA}, []byte("hlc-2"), [32]byte{0x02})
	log.InsertForTest([32]byte{3}, [32]byte{0xAA}, []byte("hlc-3"), [32]byte{0x03})
	// Log is now full (3 entries). Inserting more triggers eviction.
	log.InsertForTest([32]byte{4}, [32]byte{0xAA}, []byte("hlc-4"), [32]byte{0x04})
	log.InsertForTest([32]byte{5}, [32]byte{0xAA}, []byte("hlc-5"), [32]byte{0x05})

	// Verify: steward 1's entry was evicted (FIFO).
	if log.HasForTest([32]byte{1}, [32]byte{0xAA}) {
		t.Error("steward 1 should have been evicted (oldest)")
	}
	if !log.HasForTest([32]byte{5}, [32]byte{0xAA}) {
		t.Error("steward 5 should still be present (newest)")
	}

	// Equivocation check at the evicted slot should NOT detect
	// conflict — the entry is gone.
	conflict := log.CheckForTest([32]byte{1}, [32]byte{0xAA}, []byte("hlc-conflict"), [32]byte{0xFF})
	if conflict {
		t.Error("conflict should NOT be detected at evicted (steward, prior) — entry was dropped")
	}
	t.Logf("FIFO eviction confirmed: oldest entry dropped, conflict detection at evicted slot returns false")
}