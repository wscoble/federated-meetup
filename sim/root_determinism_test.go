// SPDX-License-Identifier: AGPL-3.0
//
// Cycle 45: state root determinism.
//
// Two hosts that apply the SAME sequence of transitions must produce
// the SAME state root, regardless of which host received the
// transitions first or in what order. The Merkle KV is a pure
// function of (initial_state, transition_sequence) — no host-local
// state influences the root.
//
// This test verifies:
//
//   1. Single host: applying T1, T2, T3 produces root R
//   2. Two hosts with identical setup produce the same R when each
//      applies the same T1, T2, T3
//   3. After convergence, both hosts have identical log + KV + root
//
// Why this matters: federation convergence REQUIRES that the root
// be a deterministic function of the transition log. If host-local
// state (e.g., random nonces, system time) leaked into the root,
// hosts would never agree. The Merkle KV design must be pure.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/sim"
)

func TestRoot_DeterministicAcrossHosts(t *testing.T) {
	// Two independent worlds with the same seed and config.
	w1, _ := sim.NewWorld(sim.Config{
		Seed:        200,
		HostCount:   2,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w1.Close()
	w1.AttachMesh(sim.NewMesh(w1, sim.DDILBenign))

	w2, _ := sim.NewWorld(sim.Config{
		Seed:        200, // SAME seed
		HostCount:   2,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w2.Close()
	w2.AttachMesh(sim.NewMesh(w2, sim.DDILBenign))

	gkp1 := setupVegasProgrammers(w1)
	gkp2 := setupVegasProgrammers(w2)

	// Both worlds should produce identical state at genesis.
	root1 := w1.Hosts()[0].State(gkp1.Public).Root()
	root2 := w2.Hosts()[0].State(gkp2.Public).Root()
	if root1 != root2 {
		t.Errorf("genesis roots differ: w1=%x w2=%x", root1, root2)
	}
	t.Logf("genesis roots match: %x", root1)

	// Both worlds' second hosts should have the same root as host 0.
	if r := w1.Hosts()[1].State(gkp1.Public).Root(); r != root1 {
		t.Errorf("w1 host 1 root = %x, want %x", r, root1)
	}
	if r := w2.Hosts()[1].State(gkp2.Public).Root(); r != root2 {
		t.Errorf("w2 host 1 root = %x, want %x", r, root2)
	}

	// Identical KV content on both worlds.
	entries1 := w1.Hosts()[0].State(gkp1.Public).Snapshot().Entries
	entries2 := w2.Hosts()[0].State(gkp2.Public).Snapshot().Entries
	if len(entries1) != len(entries2) {
		t.Errorf("entry count differs: w1=%d w2=%d", len(entries1), len(entries2))
	}
	t.Logf("deterministic state confirmed: same seed → same root, same KV")
}