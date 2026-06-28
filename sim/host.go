// SPDX-License-Identifier: MIT
//
// Package sim: Host is the virtual host inside the simulator.
//
// A Host models the state of one host in the federation: which groups it
// serves, which transitions it has applied, its current view of every group
// it has heard about. The Host is connected to the virtual mesh; sending a
// transition to another host enqueues a Message on the mesh.

package sim

import (
	"sync"
	"time"

	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/hlc"
	"github.com/sscoble/federated-meetup/internal/types"
)

// Host is one virtual host on the simulated mesh.
type Host struct {
	id    string
	world *World
	mesh  *Mesh

	mu sync.Mutex

	// hlcCursor is this host's last-issued HLC. Updated on every
	// SubmitTransition (Tick) and every Deliver (Observe). The cursor
	// preserves strict monotonicity across the host's lifetime — even
	// when wall-clock goes backwards (DDILClockSkew), the cursor never
	// regresses.
	hlcCursor hlc.HLC

	// clockSkew is this host's wall-clock offset from world.Now().
	// Positive = host clock is ahead; negative = behind. Defaults to 0
	// (in sync). Set via SetClockSkew for fault injection.
	clockSkew time.Duration

	// Groups this host serves, keyed by group ID. A host may serve any
	// number of groups; in the simulator we give each host every group
	// (i.e. every host is a full mirror), but the API allows partial hosting.
	groups map[types.GroupID]*group.State

	// Outbound transitions this host has authored, for assertion/debug.
	outbound []*group.Transition
}

// NewHost creates a virtual host. Called by World.
func NewHost(id string, w *World) *Host {
	return &Host{
		id:     id,
		world:  w,
		groups: make(map[types.GroupID]*group.State),
	}
}

// AttachMesh connects the host to the virtual mesh.
func (h *Host) AttachMesh(m *Mesh) { h.mesh = m }

// ID returns the host's identifier.
func (h *Host) ID() string { return h.id }

// World returns the simulator world this host belongs to.
func (h *Host) World() *World { return h.world }

// Groups returns the groups this host serves (read-only snapshot).
func (h *Host) Groups() []types.GroupID {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]types.GroupID, 0, len(h.groups))
	for k := range h.groups {
		out = append(out, k)
	}
	return out
}

// State returns the host's current view of a group, or nil.
func (h *Host) State(g types.GroupID) *group.State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.groups[g]
}

// AddGroup registers a new group on this host with the given initial
// transition. The transition is NOT yet applied — call SubmitTransition.
func (h *Host) AddGroup(gid types.GroupID, t *group.Transition) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.groups[gid]; !ok {
		h.groups[gid] = group.NewState(gid)
	}
}

// SubmitTransition applies a transition locally and (if meshed) broadcasts
// it to the other hosts. Returns the new state after applying.
//
// The transition is stamped with this host's current HLC before broadcast.
// The HLC is computed from the host's wall clock (which may be skewed from
// world.Now() per clockSkew) and advanced to maintain monotonicity.
func (h *Host) SubmitTransition(g types.GroupID, t *group.Transition) (*group.State, error) {
	h.mu.Lock()
	gid := t.GroupID()
	st, ok := h.groups[gid]
	if !ok {
		h.mu.Unlock()
		return nil, ErrUnknownGroup
	}

	// Tick the host's HLC with this host's wall clock (world.Now() +
	// clockSkew). The HLC has its own monotonicity guarantees — even if
	// clockSkew takes us backwards, the cursor advances forward.
	hostNow := h.world.Now().Add(h.clockSkew)
	next, err := hlc.Tick(h.hlcCursor, hostNow)
	if err != nil {
		h.mu.Unlock()
		return nil, err
	}
	h.hlcCursor = next
	hlcBytes := next.Clone()

	if err := st.Apply(t, h.world.Now()); err != nil {
		h.mu.Unlock()
		return nil, err
	}
	// Stamp the HLC onto the proto before broadcast so receivers can
	// Observe() against it.
	t.Proto.Hlc = hlcBytes
	h.outbound = append(h.outbound, t)
	msg := group.EncodeTransition(t)
	h.mu.Unlock()

	// Broadcast (outside the lock).
	if h.mesh != nil {
		h.mesh.Send(Message{
			From:    HostID(h.id),
			To:      "*",
			Payload: msg,
			Tag:     "transition",
		})
	}
	return st, nil
}

// Deliver handles inbound messages. Called by the world's Tick.
//
// Before applying, this host Observes the remote HLC — its local HLC
// cursor advances to ensure it never issues a value <= the remote's.
// This is what makes the federation's HLC values totally ordered.
func (h *Host) Deliver(payload []byte) error {
	// The mesh doesn't carry the group ID; for now we route by the host's
	// known groups. DecodeTransition needs the group ID, so we try each.
	// For v1 we decode without a group ID by guessing from the prior_state.
	t, err := group.DecodeTransition(payload, types.GroupID{})
	if err != nil {
		return err
	}

	// Observe the remote HLC. The host's wall clock may be skewed; HLC
	// Observe is robust to that — it merges the prior cursor, the
	// remote HLC, and the host's wall clock to produce a value greater
	// than all three.
	var remoteHLC hlc.HLC
	if len(t.Proto.GetHlc()) > 0 {
		remoteHLC, err = hlc.FromProto(t.Proto.GetHlc())
		if err != nil {
			return err
		}
	}
	hostNow := h.world.Now().Add(h.clockSkew)
	h.mu.Lock()
	next, err := hlc.Observe(h.hlcCursor, remoteHLC, hostNow)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	h.hlcCursor = next
	h.mu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()
	gid := t.GroupID()
	st, ok := h.groups[gid]
	if !ok {
		// Host doesn't serve this group yet. Add it.
		st = group.NewState(gid)
		h.groups[gid] = st
	}
	return st.Apply(t, h.world.Now())
}

// SetClockSkew sets this host's wall-clock offset from the simulator's
// virtual time. Used for fault injection — DDILClockSkew profiles snap
// hosts forward or backward to verify HLC ordering survives.
func (h *Host) SetClockSkew(skew time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clockSkew = skew
}

// ClockSkew returns this host's wall-clock offset.
func (h *Host) ClockSkew() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.clockSkew
}

// HLCCursor returns this host's current HLC cursor. Used by tests to
// verify ordering invariants.
func (h *Host) HLCCursor() hlc.HLC {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hlcCursor.Clone()
}

// Tick advances the host one virtual timestep. Host simulation does not
// own the wg mesh; if a sim test wants to drive group-state changes
// through the same mesh, it should use a separate mechanism.
func (h *Host) Tick() {
	// No-op by default. Hosts can override behavior via their own
	// simulation logic; the simulator's job is just to advance time
	// and let consumers (test code, wg transport) react.
}

// PeekMessages returns any messages pending in the mesh without draining
// them. Useful when multiple consumers (sim hosts + wg wire) share a mesh
// and you don't want one's polling to starve another.
func (h *Host) PeekMessages() []Message {
	if h.mesh == nil {
		return nil
	}
	return h.mesh.Peek()
}

// ErrUnknownGroup is returned when a host receives a transition for a group
// it has never heard of.
var ErrUnknownGroup = &SimError{Kind: "unknown_group", Msg: "host does not serve this group"}

// SimError is a structured error from the simulator.
type SimError struct {
	Kind string
	Msg  string
}

func (e *SimError) Error() string { return e.Kind + ": " + e.Msg }