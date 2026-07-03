// SPDX-License-Identifier: AGPL-3.0
//
// Package sim is the VOPR-style deterministic simulator for federated-meetup.
//
// Design principles (validated with Scott, 2026-06-24/26):
//
//  1. Deterministic time. The sim owns the clock. Hosts ask the sim for the
//     current time. No time.Now() in host code.
//
//  2. Deterministic RNG. The sim seeds everything. Same seed = same scenario.
//
//  3. Fault injection as a first-class concern. DDIL (Denied, Disrupted,
//     Intermittent, Limited) is part of the API, not a debug afterthought.
//
//  4. The sim spins up N hosts on a virtual mesh, drives actions, and asserts
//     invariants. The same seed always produces the same sequence of events.
//
//  5. Tests are scenarios, not assertions on a single state. A scenario is a
//     seed + an initial setup + a sequence of actions + a set of invariants.
//     The sim replays scenarios and reports the first invariant violation.
//
// Usage:
//
//	w, _ := sim.NewWorld(sim.Config{Seed: 42, HostCount: 4})
//	defer w.Close()
//	mesh := sim.NewMesh(w, sim.DDILAggressive)
//	w.AttachMesh(mesh)
//	w.Advance(sim.Duration(10 * time.Second))
package sim

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	mrand "math/rand/v2"
	"sync"
	"time"
)

// World is the deterministic simulation world.
type World struct {
	mu sync.Mutex

	// masterSeed is the seed value from Config.Seed. Stored separately
	// from the RNG so DeriveSeed can produce deterministic, repeatable
	// outputs without consuming RNG state.
	masterSeed uint64

	// Deterministic RNG (Go's math/rand/v2 source for forkability).
	rng *mrand.Rand

	// Virtual clock. Time only advances when Advance() is called.
	now time.Time

	// mesh is the virtual wire between hosts. Set by AttachMesh; nil until
	// then.
	mesh *Mesh

	// Hosts in the simulation. Lazily created via NewHost.
	hosts []*Host
}

// Config is the simulator configuration.
type Config struct {
	// Seed is the master seed for all randomness.
	Seed uint64

	// HostCount is the number of hosts to spin up. Hosts are identified
	// "h0", "h1", ..., "hN-1".
	HostCount int

	// InitialTime sets the wall-clock the sim starts at. Defaults to
	// 2026-01-01 UTC if zero.
	InitialTime time.Time
}

// NewWorld creates a deterministic simulation world. The mesh is not attached;
// call AttachMesh before driving traffic.
func NewWorld(cfg Config) (*World, error) {
	if cfg.HostCount < 1 {
		return nil, fmt.Errorf("sim: HostCount must be >= 1, got %d", cfg.HostCount)
	}
	if cfg.Seed == 0 {
		return nil, fmt.Errorf("sim: Seed must be non-zero")
	}
	if cfg.InitialTime.IsZero() {
		cfg.InitialTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	w := &World{
		masterSeed: cfg.Seed,
		rng:        mrand.New(mrand.NewPCG(cfg.Seed, cfg.Seed^0x9E3779B97F4A7C15)),
		now:        cfg.InitialTime,
	}
	w.hosts = make([]*Host, cfg.HostCount)
	for i := 0; i < cfg.HostCount; i++ {
		hostID := fmt.Sprintf("h%d", i)
		w.hosts[i] = NewHost(hostID, w)
	}
	return w, nil
}

// AttachMesh connects all hosts to the given mesh.
func (w *World) AttachMesh(m *Mesh) {
	w.mesh = m
	for _, h := range w.hosts {
		h.AttachMesh(m)
	}
}

// Mesh returns the virtual mesh between hosts, or nil if not attached.
func (w *World) Mesh() *Mesh { return w.mesh }

// Close shuts the world down. Currently a no-op, but gives callers a hook to
// flush logs / release resources.
func (w *World) Close() error { return nil }

// Now returns the simulator's virtual time. ALL host code asks the world
// for time, not time.Now().
func (w *World) Now() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.now
}

// Advance moves the virtual clock forward by d. During the advance, the mesh
// may partition hosts per its DDIL profile, and each host ticks once
// (peeks any messages whose delivery time has arrived; the host does not
// drain them — only the owner of the destination drains).
func (w *World) Advance(d time.Duration) {
	w.mu.Lock()
	w.now = w.now.Add(d)
	w.mu.Unlock()
	if w.mesh != nil {
		w.mesh.MaybePartition()
	}
	for _, h := range w.hosts {
		h.Tick()
	}
}

// Tick advances the world by zero time — delivers any messages whose delivery
// time has already arrived and partitions per the DDIL profile.
func (w *World) Tick() { w.Advance(0) }

// RandInt returns a deterministic random int in [0, n). Used by hosts to
// drive non-deterministic scheduling under simulator control.
func (w *World) RandInt(n int) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rng.IntN(n)
}

// RandUint32 returns a deterministic random uint32.
func (w *World) RandUint32() uint32 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.rng.Uint32()
}

// RandBytes returns a deterministic random byte slice of length n.
func (w *World) RandBytes(n int) []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(w.rng.Uint32())
	}
	return out
}

// DeriveSeed derives a child seed from the world's seed + a label. Used by
// hosts/users to get deterministic keys. Determinism guarantee: calling
// DeriveSeed with the same (world.seed, label) ALWAYS returns the same
// derived seed, regardless of prior RNG state. This means callers can
// re-derive keys safely without burning RNG.
func (w *World) DeriveSeed(label string) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	h := sha256.New()
	var worldSeed [8]byte
	binary.LittleEndian.PutUint64(worldSeed[:], w.seed())
	h.Write(worldSeed[:])
	h.Write([]byte(label))
	sum := h.Sum(nil)
	return binary.LittleEndian.Uint64(sum[:8])
}

// seed returns the world's master seed (read-only). Used by DeriveSeed
// so the derivation is deterministic regardless of RNG state.
func (w *World) seed() uint64 {
	// The world stores its seed implicitly via the PCG constructor.
	// For now, expose it via a field — see Config.Seed.
	return w.masterSeed
}

// Hosts returns the hosts in the world. The returned slice is the world's
// internal slice; callers must not modify it.
func (w *World) Hosts() []*Host { return w.hosts }

// HostByID returns the host with the given ID, or nil.
func (w *World) HostByID(id string) *Host {
	for _, h := range w.hosts {
		if h.id == id {
			return h
		}
	}
	return nil
}