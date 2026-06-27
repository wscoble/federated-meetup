// SPDX-License-Identifier: MIT
//
// Package sim: virtual mesh. Models the WireGuard overlay between hosts.
//
// Each pair of hosts has a virtual link. The link has a DDIL profile. When a
// host sends a message, the mesh:
//   1. Computes the delivery latency (base + jitter).
//   2. Rolls for drop / corrupt / reorder.
//   3. Schedules delivery at the simulator's virtual time.
//
// The mesh is what makes the simulator honest about network failures. The
// host never gets to call "send" synchronously and have the message arrive;
// the mesh decides when (and whether) it arrives.

package sim

import (
	"sync"
	"time"
)

// HostID identifies a host on the mesh.
type HostID string

// Message is a unit of communication between hosts. In the simulator, this is
// the proto-level transition or RPC envelope. The mesh treats it as opaque
// bytes — it doesn't care about content.
type Message struct {
	From      HostID
	To        HostID
	Payload   []byte
	Sent      time.Time
	// Tag is a hint for the receiver. Used by the test harness to match
	// expected vs. delivered messages.
	Tag string
}

// Mesh is the virtual wire between hosts.
type Mesh struct {
	mu sync.Mutex
	w  *World

	profile DDILProfile

	// Per-link state: in-flight messages, partitions.
	inFlight []*inFlightMessage

	// partitionSet tracks pairs of hosts currently partitioned. A
	// partition is a full drop: messages between two partitioned hosts
	// never arrive (they're dropped immediately).
	partitions map[hostPair]time.Time
}

type hostPair struct {
	from, to HostID
}

type inFlightMessage struct {
	msg     Message
	deliver time.Time
}

// NewMesh creates the virtual mesh for the world.
func NewMesh(w *World, profile DDILProfile) *Mesh {
	return &Mesh{
		w:          w,
		profile:    profile,
		partitions: make(map[hostPair]time.Time),
	}
}

// Send enqueues a message for delivery. Returns immediately. The actual
// delivery happens at simulator time = now + computed latency, subject to
// drop / reorder / partition rules.
func (m *Mesh) Send(msg Message) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.w.Now()
	from := msg.From
	to := msg.To

	// Apply partition check.
	if expiry, ok := m.partitions[hostPair{from, to}]; ok {
		if now.Before(expiry) {
			// Partition still active — drop on the floor.
			return
		}
		// Partition expired.
		delete(m.partitions, hostPair{from, to})
	}

	// Roll for corruption/drop. We treat corruption as a drop because the
	// wire layer (WireGuard's authenticated UDP) discards corrupted packets.
	r := m.w.RandInt(100000)
	dropRoll := int(m.profile.DropProb * 100000)
	corruptRoll := int(m.profile.CorruptProb * 100000)
	if r < dropRoll+corruptRoll {
		// Dropped.
		return
	}

	// Compute latency.
	latency := m.profile.BaseLatency
	if m.profile.Jitter > 0 {
		jitterNanos := int64(m.w.RandInt(int(m.profile.Jitter)))
		latency += time.Duration(jitterNanos)
	}

	// Reorder: instead of scheduling for `now + latency`, schedule for
	// `now + 2*latency` with probability ReorderProb.
	if m.profile.ReorderProb > 0 {
		reorderRoll := m.w.RandInt(100000)
		if int(reorderRoll) < int(m.profile.ReorderProb*100000) {
			latency *= 2
		}
	}

	msg.Sent = now
	m.inFlight = append(m.inFlight, &inFlightMessage{
		msg:     msg,
		deliver: now.Add(latency),
	})
}

// Poll delivers any messages whose delivery time has arrived and removes
// them from the in-flight queue. Called by the world's Advance() or by the
// host explicitly.
func (m *Mesh) Poll() []Message {
	return m.poll(true)
}

// Peek returns messages whose delivery time has arrived WITHOUT removing
// them. Use this when multiple consumers share a mesh and you don't want
// one consumer's Poll to starve another.
func (m *Mesh) Peek() []Message {
	return m.poll(false)
}

func (m *Mesh) poll(remove bool) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.w.Now()
	var delivered []Message
	var remaining []*inFlightMessage
	for _, inf := range m.inFlight {
		if !inf.deliver.After(now) {
			delivered = append(delivered, inf.msg)
		} else {
			remaining = append(remaining, inf)
		}
	}
	if remove {
		m.inFlight = remaining
	}
	return delivered
}

// MaybePartition randomly partitions a pair of hosts per the DDIL profile.
// Called once per Advance() tick.
func (m *Mesh) MaybePartition() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.profile.PartitionProb <= 0 {
		return
	}
	roll := m.w.RandInt(100000)
	if int(roll) >= int(m.profile.PartitionProb*100000) {
		return
	}
	// Pick two distinct hosts.
	hosts := m.w.Hosts()
	if len(hosts) < 2 {
		return
	}
	a := hosts[m.w.RandInt(len(hosts))].id
	b := hosts[m.w.RandInt(len(hosts))].id
	for a == b {
		b = hosts[m.w.RandInt(len(hosts))].id
	}
	expiry := m.w.Now().Add(m.profile.PartitionDuration)
	m.partitions[hostPair{HostID(a), HostID(b)}] = expiry
	m.partitions[hostPair{HostID(b), HostID(a)}] = expiry
}

// Profile returns the DDIL profile of the mesh.
func (m *Mesh) Profile() DDILProfile { return m.profile }