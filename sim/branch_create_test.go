// SPDX-License-Identifier: AGPL-3.0
//
// BRANCH_CREATE scenario: a steward group creates a new branch
// within itself (the "cheap fork" — same group, divergent evolution
// path). The new branch can later be addressed via its branch_id.
//
// What this exercises:
//   - BRANCH_CREATE transition on the parent branch (records branch
//     allocation in the parent's KV)
//   - branch_registry: new branch exists with the right reason
//   - Cross-host convergence on the post-BRANCH_CREATE root
//
// Why this matters: BRANCH_CREATE is the protocol primitive for
// in-group disagreement. Stewards disagree on direction but don't
// want to sovereign-split (FORK) — they create a branch, evolve
// it independently, and downstream users pick which branch to
// follow.
//
// Note: mutations on non-genesis branches are NOT yet wired (see
// group.go line ~360 — "branch-local mutations on non-genesis
// branches not yet wired"). This test only verifies BRANCH_CREATE
// itself allocates the branch and is observable to all hosts.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
)

// TestBranchCreate_CheapFork walks through:
//  1. Vegas Programmers exist (parent group, branch 0)
//  2. Stewards sign BRANCH_CREATE with reason "alternative-meetup-format"
//  3. All hosts apply → state root advances
//  4. Verify each host has a new branch in its registry
func TestBranchCreate_CheapFork(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        46,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	mesh := sim.NewMesh(w, sim.DDILBenign)
	w.AttachMesh(mesh)

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	// Sanity: parent converged on all hosts.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-branch: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}
	t.Logf("parent root pre-branch: %x", parentRoot)

	// BRANCH_CREATE: stewards alice + bob sign it. Reason: a human-readable
	// rationale surfaced in BRANCH_LIST responses.
	reason := "alternative-meetup-format"
	if !applyBroadcastFor(t, w, gkp.Public, "BRANCH_CREATE "+reason,
		pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE,
		&pb.BranchCreatePayload{
			ParentSnapshot: nil, // optional in proto
			Reason:         reason,
		},
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Verify root advanced on all hosts.
	branchedRoot := w.Hosts()[0].State(gkp.Public).Root()
	if branchedRoot == parentRoot {
		t.Fatalf("BRANCH_CREATE did not advance root (still %x)", parentRoot)
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != branchedRoot {
			t.Fatalf("post-BRANCH_CREATE divergence: host %s=%x want %x", h.ID(), got, branchedRoot)
		}
	}
	t.Logf("parent root post-branch: %x", branchedRoot)

	// Verify the branch_create entry exists in the snapshot KV.
	entries := w.Hosts()[0].State(gkp.Public).Snapshot().Entries
	var foundBranchEntry bool
	var foundReasonEntry bool
	for _, e := range entries {
		if e.Key == "branch/1/reason" {
			foundReasonEntry = true
			if !strings.Contains(string(e.Value), reason) {
				t.Errorf("branch/1/reason = %q, want contains %q", string(e.Value), reason)
			}
		}
		if e.Key == "branch/1/parent" {
			foundBranchEntry = true
		}
	}
	if !foundReasonEntry {
		t.Errorf("branch/1/reason entry not found; entries = %v", entries)
	}
	if !foundBranchEntry {
		t.Errorf("branch/1/parent entry not found; entries = %v", entries)
	}

	// Final convergence check.
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != branchedRoot {
			t.Errorf("post-BRANCH_CREATE final divergence: %s=%x want %x", h.ID(), got, branchedRoot)
		}
	}
}