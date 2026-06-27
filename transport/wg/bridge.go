// SPDX-License-Identifier: MIT
//
// Bridge between sim.Mesh and the wg peers' simBinds.
//
// The bridge is the chokepoint that forces every wg transport packet to
// traverse sim.Mesh (and therefore the DDIL profile). It runs as a set of
// goroutines:
//
//   - One sender goroutine per peer: drains bind.sendCh and pushes packets
//     into sim.Mesh with the destination peer's ID.
//   - One receiver goroutine total: polls sim.Mesh deliveries and injects
//     them into the destination peer's simBind via inject().
//
// In production, the bridge is replaced by a real UDP socket reading from
// the wg interface; the sim.Mesh is replaced by the actual Internet. The
// simBind and bind interface are unchanged.

package wg

import (
	"github.com/sscoble/federated-meetup/sim"
)

// RunBridge starts the bridge goroutines and returns a function that
// stops them. The caller is responsible for advancing the sim.World's
// virtual clock so sim.Mesh deliveries become eligible.
//
// The returned function is idempotent.
func (m *Mesh) RunBridge(world *sim.World) (stop func(), err error) {
	m.mu.Lock()
	if m.bridgeRunning {
		m.mu.Unlock()
		return func() {}, nil
	}
	m.bridgeRunning = true
	binds := make(map[PeerID]*simBind, len(m.peerBinds))
	for id, b := range m.peerBinds {
		binds[id] = b
	}
	m.mu.Unlock()

	stopCh := make(chan struct{})
	m.bridgeStop = stopCh

	// Per-peer sender goroutines: each reads from bind.sendCh and pushes
	// into sim.Mesh.
	for id, bind := range binds {
		go m.bridgeSender(id, bind, world, stopCh)
	}

	// Single receiver goroutine: polls sim.Mesh and delivers.
	go m.bridgeReceiver(world, stopCh)

	return func() {
		select {
		case <-stopCh:
			return
		default:
			close(stopCh)
		}
	}, nil
}

func (m *Mesh) bridgeSender(id PeerID, bind *simBind, world *sim.World, stopCh <-chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		case <-bind.done:
			return
		case pkt := <-bind.sendCh:
			// Push into sim.Mesh. The mesh schedules delivery with
			// DDIL profile (drop/latency/jitter/reorder/partition).
			world.Mesh().Send(sim.Message{
				From:    sim.HostID(string(id)),
				To:      sim.HostID(string(pkt.to.peerID)),
				Payload: pkt.data,
				Tag:     "wg",
			})
		}
	}
}

func (m *Mesh) bridgeReceiver(world *sim.World, stopCh <-chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		default:
		}
		// Drain any messages whose delivery time has arrived. Use Poll
		// (not Peek) since we're the sole consumer of the wg mesh path.
		for _, msg := range world.Mesh().Poll() {
			to := PeerID(msg.To)
			if err := m.Deliver(to, msg.Payload); err != nil {
				// No bind yet for this peer — packet dropped. This is
				// normal during bootstrap.
				_ = err
			}
		}
		// Don't spin: yield. The test harness drives Advance() to make
		// time pass; we just poll whenever called.
		// Small sleep keeps CPU sane in tests.
		select {
		case <-stopCh:
			return
		default:
		}
	}
}