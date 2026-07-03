// SPDX-License-Identifier: AGPL-3.0
//
// Package host — multi-group registry.
//
// At v0 the host service was bound to a single *group.State — the
// "home group" — and the ConnectRPC handler looked up that one state
// for every request. A real host runs many groups: each group has
// its own state machine, its own steward set, its own log.
//
// MultiGroup is the in-memory registry that maps GroupID to
// *group.State. It is intentionally minimal:
//
//   - Thread-safe reads (RWMutex).
//   - GroupID is the canonical lookup key.
//   - Insert/Remove are not exposed: the host's source of truth for
//     "which groups do I host" is a future cycle's bootstrap loader
//     (config file, gossip-fed peer list, on-disk state). v0 only
//     supports the constructor + lookup + list.
//   - Name → GroupID resolution is a placeholder; real name
//     resolution comes in a later cycle (canonical names live in the
//     state KV, not in a separate directory at v0).
//
// The ConnectRPC handler (Service) holds a *MultiGroup and resolves
// the GroupID from the request's GroupKey. If a request names a
// group not hosted here, the handler returns NotFound.
package host

import (
	"sync"

	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/types"
)

// MultiGroup is a thread-safe map of GroupID → *group.State.
//
// It is the host's "which groups am I currently serving" registry.
// At v0 it is populated at startup from a config-supplied list;
// future cycles will populate it dynamically (gossip, peer sync).
type MultiGroup struct {
	mu    sync.RWMutex
	byKey map[types.GroupID]*group.State
}

// NewMultiGroup constructs a registry pre-populated with the given
// states. The states are stored by their GroupID() (the value the
// state machine was constructed with, not the caller's key).
//
// The name parameter is the host's canonical name (used by
// ResolveName responses). One name per host, regardless of how many
// groups the host serves.
func NewMultiGroup(name string, states ...*group.State) *MultiGroup {
	mg := &MultiGroup{
		byKey: make(map[types.GroupID]*group.State, len(states)),
	}
	for _, s := range states {
		if s == nil {
			continue
		}
		mg.byKey[s.GroupID()] = s
	}
	_ = name // see ResolveName for how name is used.
	return mg
}

// Get returns the *group.State for the given group, or (nil, false)
// if this host does not serve that group.
func (m *MultiGroup) Get(gid types.GroupID) (*group.State, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.byKey[gid]
	return s, ok
}

// All returns a snapshot of all (GroupID, *group.State) pairs the
// host currently serves. The slice and map are safe to iterate;
// callers must not retain the *group.State references past the
// lifetime of the underlying state machine (use Apply through the
// state, not direct field access).
func (m *MultiGroup) All() []types.GroupID {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]types.GroupID, 0, len(m.byKey))
	for gid := range m.byKey {
		out = append(out, gid)
	}
	return out
}

// Len returns the number of groups currently hosted.
func (m *MultiGroup) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.byKey)
}
