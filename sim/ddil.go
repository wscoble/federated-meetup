// SPDX-License-Identifier: AGPL-3.0
//
// Package sim: DDIL fault profiles.
//
// DDIL = Denied, Disrupted, Intermittent, Limited bandwidth. The protocol
// runs over a private WireGuard mesh, but the simulator models what happens
// when the underlying network degrades. This is a first-class concern because
// the federation has to survive flaky links — Vegas to Phoenix to Scottsdale
// is a real geography with real DDIL conditions.
//
// Reference: DOD Directive 8100.01 / NATO AJMEDP-7 framing. We don't claim
// rigor — we claim the simulator makes DDIL a knob, not an accident.

package sim

import "time"

// DDILProfile models the network conditions between hosts.
type DDILProfile struct {
	// Probability [0,1] that a given packet is dropped (Denied).
	DropProb float64
	// Probability [0,1] that a packet is corrupted (Disrupted). The wire
	// layer detects this and drops it, but it counts toward reorder.
	CorruptProb float64
	// Base latency added to every message.
	BaseLatency time.Duration
	// Jitter — uniformly distributed [0, Jitter).
	Jitter time.Duration
	// Probability [0,1] of reordering (a packet arrives after a later one).
	ReorderProb float64
	// Bandwidth limit in bytes/second. 0 = unlimited.
	BandwidthBPS int
	// Probabilistic partitions: every Advance, with this probability, a
	// random pair of hosts becomes fully partitioned for `PartitionDuration`.
	PartitionProb float64
	PartitionDuration time.Duration
}

// Standard profiles.
var (
	// DDILBenign: a healthy datacenter LAN. ~0% loss, sub-ms latency.
	DDILBenign = DDILProfile{
		DropProb:     0.0,
		CorruptProb:  0.0,
		BaseLatency:  500 * time.Microsecond,
		Jitter:       100 * time.Microsecond,
		ReorderProb:  0.0,
		BandwidthBPS: 0,
	}

	// DDILMild: typical consumer internet. Occasional loss, jitter, ~50ms RTT.
	DDILMild = DDILProfile{
		DropProb:     0.005,
		CorruptProb:  0.0,
		BaseLatency:  25 * time.Millisecond,
		Jitter:       10 * time.Millisecond,
		ReorderProb:  0.001,
		BandwidthBPS: 100 * 1024 * 1024,
	}

	// DDILAggressive: contested network. Frequent loss, high latency,
	// periodic partitions. Models wartime / disaster conditions.
	DDILAggressive = DDILProfile{
		DropProb:     0.05,
		CorruptProb:  0.01,
		BaseLatency:  200 * time.Millisecond,
		Jitter:       100 * time.Millisecond,
		ReorderProb:  0.05,
		BandwidthBPS: 10 * 1024 * 1024,
		PartitionProb:     0.01,
		PartitionDuration: 5 * time.Second,
	}

	// DDILHostile: near-total denial. Used to verify the system cannot
	// accidentally depend on synchronous server-to-server communication.
	DDILHostile = DDILProfile{
		DropProb:     0.3,
		CorruptProb:  0.05,
		BaseLatency:  1 * time.Second,
		Jitter:       500 * time.Millisecond,
		ReorderProb:  0.1,
		BandwidthBPS: 1 * 1024 * 1024,
		PartitionProb:     0.1,
		PartitionDuration: 30 * time.Second,
	}
)