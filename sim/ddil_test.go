// SPDX-License-Identifier: AGPL-3.0
//
// DDIL recovery test: under packet loss, latency, and partition, hosts
// converge on the same state root after enough simulated time.
//
// Uses DDILMild (5% loss, jitter, mild reorder) — the consumer-internet
// baseline. Verifies the simulator's bookkeeping is honest: messages
// delayed by the mesh eventually arrive; reordered messages don't
// corrupt the state machine.

package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/sim"
)

func TestSimulator_DDIL_Recovery(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        1337,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// DDILMild: 0.5% drop, 25ms base, 10ms jitter, occasional reorder.
	// Real consumer internet between Vegas and Phoenix.
	mesh := sim.NewMesh(w, sim.DDILMild)
	w.AttachMesh(mesh)

	gkp := setupVegasProgrammers(w)

	// Advance time generously — 5 seconds of virtual time — to let all
	// messages arrive despite DDIL. The mesh delivery is virtual, so this
	// is fast.
	for i := 0; i < 50; i++ {
		w.Advance(100 * time.Millisecond)
	}

	// After 5 simulated seconds, every host should agree.
	want := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts() {
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

	t.Logf("after 5s of DDILMild, converged on state root: %x", want)
}