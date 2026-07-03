// SPDX-License-Identifier: AGPL-3.0
//
// Migration scenario: a group moves its canonical host from h0 to
// h2 after a deadline. Mirrors (h1, h3) continue serving read
// traffic; writes go to h2 after the deadline.
//
// What this exercises:
//   - MIGRATE transition on the group state
//   - canonical_host + canonical_after entries in the Merkle KV
//   - Cross-host convergence on the post-migration root
//
// Why this matters: migration is the operational primitive for
// moving a group's primary host without losing state. Federation
// needs this because hosts can fail, get decommissioned, or have
// hardware replaced. The protocol must support planned handoff
// with zero downtime via mirrors.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestMigrate_CanonicalHostTransfer walks through:
//  1. Vegas Programmers exist on all 4 hosts (parent group)
//  2. Stewards sign a MIGRATE transition declaring h2 as the new canonical host
//  3. All hosts apply the MIGRATE → state root advances, canonical_host entry exists
//  4. Hosts still converge on the post-migration root
func TestMigrate_CanonicalHostTransfer(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        44,
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

	// Sanity: parent group converged across all hosts.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-migrate: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}
	t.Logf("parent root pre-migrate: %x", parentRoot)

	// Pick h2 as the new canonical host.
	newCanonical := w.Hosts()[2].ID()
	t.Logf("new canonical host: %s", newCanonical)

	// Build and broadcast MIGRATE.
	if !applyBroadcastFor(t, w, gkp.Public, "MIGRATE to "+newCanonical,
		pb.TransitionType_TRANSITION_TYPE_MIGRATE,
		&pb.MigratePayload{
			NewHost:  newCanonical,
			Deadline: timestamppb.New(w.Now().Add(24 * time.Hour)),
		},
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Verify root advanced on all hosts.
	migratedRoot := w.Hosts()[0].State(gkp.Public).Root()
	if migratedRoot == parentRoot {
		t.Fatalf("MIGRATE did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != migratedRoot {
			t.Fatalf("post-migrate divergence: host %s=%x want %x", h.ID(), got, migratedRoot)
		}
	}
	t.Logf("parent root post-migrate: %x", migratedRoot)

	// Verify canonical_host entry exists in the snapshot.
	entries := w.Hosts()[0].State(gkp.Public).Snapshot().Entries
	var found bool
	for _, e := range entries {
		if e.Key == "canonical_host" {
			found = true
			if string(e.Value) != newCanonical {
				t.Errorf("canonical_host = %q, want %q", string(e.Value), newCanonical)
			}
		}
	}
	if !found {
		t.Errorf("canonical_host entry not found in snapshot; entries = %v", entries)
	}

	// Sanity: post-migration, all hosts still agree on the root.
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != migratedRoot {
			t.Errorf("post-migrate divergence on final check: %s=%x want %x", h.ID(), got, migratedRoot)
		}
	}
}