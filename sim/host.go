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

	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/types"
)

// Host is one virtual host on the simulated mesh.
type Host struct {
	id    string
	world *World
	mesh  *Mesh

	mu sync.Mutex

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
func (h *Host) SubmitTransition(g types.GroupID, t *group.Transition) (*group.State, error) {
	h.mu.Lock()
	gid := t.GroupID()
	st, ok := h.groups[gid]
	if !ok {
		h.mu.Unlock()
		return nil, ErrUnknownGroup
	}
	if err := st.Apply(t, h.world.Now()); err != nil {
		h.mu.Unlock()
		return nil, err
	}
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
func (h *Host) Deliver(payload []byte) error {
	// The mesh doesn't carry the group ID; for now we route by the host's
	// known groups. DecodeTransition needs the group ID, so we try each.
	// For v1 we decode without a group ID by guessing from the prior_state.
	t, err := group.DecodeTransition(payload, types.GroupID{})
	if err != nil {
		return err
	}
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

// Tick advances the host one virtual timestep: deliver any messages the mesh
// has for us, apply them to our state.
func (h *Host) Tick() {
	if h.mesh == nil {
		return
	}
	msgs := h.mesh.Poll()
	for _, m := range msgs {
		if m.To != "*" && m.To != HostID(h.id) {
			// Not for us — but in the simulator we route to all hosts that
			// serve the group. Real WireGuard would route only to the
			// destination.
			continue
		}
		_ = h.Deliver(m.Payload)
	}
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