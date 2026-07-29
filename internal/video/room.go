// SPDX-License-Identifier: AGPL-3.0
//
// Package video provides P2P video chat for church small group meetings.
//
// Architecture (v1 — mesh P2P):
//
//	Browser ←→ Browser (direct WebRTC)
//	           ↑
//	           │ signaling relay
//	           ↑
//	        Go server (WebSocket)
//
// The Go server only relays SDP offers/answers and ICE candidates between
// peers. It does not process media. Behind Tailscale, peers connect directly
// without STUN/TURN.
//
// Tier 1 (≤8 participants): Full mesh — each peer connects to every other peer.
// Tier 2 (9-20 participants): SFU stub — not yet implemented. See SFUProvider
// interface in sfu.go.
package video

import (
	"sync"
	"time"
)

// MaxMeshParticipants is the maximum number of participants supported by
// the mesh P2P topology. Above this, the SFU path should be used.
const MaxMeshParticipants = 8

// RoomOpenBefore is how long before an event's start time the video room opens.
const RoomOpenBefore = 15 * time.Minute

// RoomOpenAfter is how long after an event's start time the video room stays open.
const RoomOpenAfter = 2 * time.Hour

// Peer represents a connected WebSocket peer in a video room.
type Peer struct {
	ID       string
	Name     string
	GroupKey string
	EventID  string
	conn     PeerConn // abstracted WebSocket connection
}

// PeerConn is the interface for a WebSocket connection, enabling testing
// with mocks.
type PeerConn interface {
	WriteJSON(v interface{}) error
	ReadJSON(v interface{}) error
	Close() error
}

// Room manages the peers connected to a single video room (identified by
// group_key). Thread-safe.
type Room struct {
	mu    sync.Mutex
	peers map[string]*Peer // peer ID → peer
}

// NewRoom creates an empty video room.
func NewRoom() *Room {
	return &Room{peers: make(map[string]*Peer)}
}

// AddPeer adds a peer to the room. Returns false if the room is full
// (exceeds MaxMeshParticipants for mesh mode).
func (r *Room) AddPeer(peer *Peer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.peers) >= MaxMeshParticipants {
		return false
	}
	r.peers[peer.ID] = peer
	return true
}

// RemovePeer removes a peer by ID. Returns true if the peer was found.
func (r *Room) RemovePeer(peerID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.peers[peerID]
	delete(r.peers, peerID)
	return ok
}

// PeerCount returns the number of connected peers.
func (r *Room) PeerCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers)
}

// Peers returns a snapshot of all peers in the room.
func (r *Room) Peers() []*Peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, p)
	}
	return out
}

// PeerIDs returns the IDs of all peers in the room.
func (r *Room) PeerIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.peers))
	for id := range r.peers {
		out = append(out, id)
	}
	return out
}

// GetPeer returns a peer by ID, or nil if not found.
func (r *Room) GetPeer(peerID string) *Peer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peers[peerID]
}

// IsRoomOpen returns true if the video room should be open based on the
// event's start time. The room is open from RoomOpenBefore before start
// to RoomOpenAfter after start.
//
// now: current time
// startsAt: event start time
func IsRoomOpen(now time.Time, startsAt time.Time) bool {
	opensAt := startsAt.Add(-RoomOpenBefore)
	closesAt := startsAt.Add(RoomOpenAfter)
	return !now.Before(opensAt) && !now.After(closesAt)
}

// IsRoomStartingSoon returns true if the event starts within RoomOpenBefore
// or has already started (but room is still open). Used to show the "Join
// video call" button on the event page.
func IsRoomStartingSoon(now time.Time, startsAt time.Time) bool {
	return IsRoomOpen(now, startsAt)
}

// RoomHub manages all active video rooms, keyed by group_key.
type RoomHub struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

// NewRoomHub creates a new RoomHub.
func NewRoomHub() *RoomHub {
	return &RoomHub{rooms: make(map[string]*Room)}
}

// GetRoom returns the room for the given group_key, creating one if it doesn't exist.
func (h *RoomHub) GetRoom(groupKey string) *Room {
	h.mu.Lock()
	defer h.mu.Unlock()
	room, ok := h.rooms[groupKey]
	if !ok {
		room = NewRoom()
		h.rooms[groupKey] = room
	}
	return room
}

// RoomExists returns true if a room exists for the group_key.
func (h *RoomHub) RoomExists(groupKey string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.rooms[groupKey]
	return ok
}

// RemoveRoomIfEmpty removes a room if it has no peers. Returns true if removed.
func (h *RoomHub) RemoveRoomIfEmpty(groupKey string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	room, ok := h.rooms[groupKey]
	if !ok {
		return false
	}
	if room.PeerCount() == 0 {
		delete(h.rooms, groupKey)
		return true
	}
	return false
}

// ParticipantCount returns the number of participants in a room.
func (h *RoomHub) ParticipantCount(groupKey string) int {
	h.mu.Lock()
	room, ok := h.rooms[groupKey]
	h.mu.Unlock()
	if !ok {
		return 0
	}
	return room.PeerCount()
}