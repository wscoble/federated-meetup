// SPDX-License-Identifier: AGPL-3.0
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
	"github.com/wscoble/federated-meetup/sim"
)

// RunBridge starts the per-peer sender goroutines. The sender goroutines
// drain bind.sendCh (filled by wireguard-go) and push outgoing packets
// onto an internal queue. Drain(world) processes both directions: it
// drains the outgoing queue into sim.Mesh, and pulls delivered packets
// out of sim.Mesh into the destination peer's bind.
//
// Rationale: the simulator owns the clock. A background goroutine polling
// sim.Mesh at wall-clock speed races against the test's Advance loop and
// produces non-deterministic behavior. Tying both send and receive to
// Drain (called once per virtual time step, after Advance) makes the
// scheduler the single source of truth.
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
	m.outQueue = make([]queuedPacket, 0, 64)

	// Per-peer sender goroutines: each drains its bind.sendCh into the
	// shared outQueue. Drain flushes the queue into sim.Mesh.
	for id, bind := range binds {
		go m.bridgeSender(id, bind, stopCh)
	}

	return func() {
		select {
		case <-stopCh:
			return
		default:
			close(stopCh)
		}
	}, nil
}

// queuedPacket is the bridge's view of a packet in the outQueue — it has
// the source PeerID attached (added by bridgeSender when it picks up a
// packet from bind.sendCh).
type queuedPacket struct {
	from PeerID
	to   PeerID
	data []byte
}

// Drain flushes any pending outgoing packets into sim.Mesh, then pulls
// delivered packets out of sim.Mesh and injects them into the destination
// peer's bind. Returns the number of packets delivered.
//
// Call this once per virtual time step, after World.Advance.
func (m *Mesh) Drain(world *sim.World) int {
	// Drain the outgoing queue (filled by sender goroutines).
	m.mu.Lock()
	out := m.outQueue
	m.outQueue = make([]queuedPacket, 0, 64)
	m.mu.Unlock()
	for _, p := range out {
		world.Mesh().Send(sim.Message{
			From:    sim.HostID(string(p.from)),
			To:      sim.HostID(string(p.to)),
			Payload: p.data,
			Tag:     "wg",
		})
	}

	// Pull delivered packets and inject them into the destination bind.
	n := 0
	for _, msg := range world.Mesh().Poll() {
		to := PeerID(msg.To)
		if err := m.DeliverFrom(to, PeerID(msg.From), msg.Payload); err != nil {
			_ = err
		}
		n++
	}
	return n
}

func (m *Mesh) bridgeSender(id PeerID, bind *simBind, stopCh <-chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		case <-bind.done:
			return
		case pkt := <-bind.sendCh:
			// Queue for Drain to flush into sim.Mesh.
			m.mu.Lock()
			m.outQueue = append(m.outQueue, queuedPacket{
				from: id,
				to:   pkt.to,
				data: pkt.data,
			})
			m.mu.Unlock()
		}
	}
}