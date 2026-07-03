// SPDX-License-Identifier: AGPL-3.0
//
// VOPR-shaped deterministic simulator smoke test.
//
// Walks through:
//   1. Create a world with 4 hosts on a benign DDIL profile.
//   2. Alice creates a group with 3 stewards, threshold 2.
//   3. Apply the CREATE_GROUP transition on all 4 hosts.
//   4. Assert every host has the same state root after message delivery.
//
// Same seed → same result. Always.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/sim"
)

func TestSimulator_Smoke(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        42,
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

	// Advance the sim to deliver messages.
	w.Advance(1 * time.Second)

	// Every host should have the same state root.
	want := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		st := h.State(gkp.Public)
		if st == nil {
			t.Errorf("host %s: no state for group", h.ID())
			continue
		}
		got := st.Root()
		if got != want {
			t.Errorf("host %s: state root = %x, want %x", h.ID(), got, want)
		}
	}

	t.Logf("group state root after CREATE_GROUP: %x", want)
}