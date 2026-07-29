// SPDX-License-Identifier: AGPL-3.0
package video
import (
	"log"
	"sync"
)

// SignalMessage is the JSON message exchanged over the signaling WebSocket.
// It carries SDP offers/answers and ICE candidates between peers.
type SignalMessage struct {
	Type     string `json:"type"`               // "offer", "answer", "ice", "join", "leave", "peers", "error", "mute_all"
	From     string `json:"from,omitempty"`     // sender peer ID
	To       string `json:"to,omitempty"`       // target peer ID (empty = broadcast)
	SDP      string `json:"sdp,omitempty"`      // SDP body for offer/answer
	ICE      string `json:"ice,omitempty"`      // ICE candidate string
	ICELabel string `json:"ice_label,omitempty"` // SDP m-line label for ICE
	Name     string `json:"name,omitempty"`     // display name for join/peers
	Peers    []PeerInfo `json:"peers,omitempty"` // peer list for "peers" message
}

// PeerInfo is a peer's info sent in "peers" messages.
type PeerInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SignalingServer handles WebSocket connections for video rooms.
// It relays SDP offers/answers and ICE candidates between peers.
type SignalingServer struct {
	hub *RoomHub
	mu  sync.Mutex
}

// NewSignalingServer creates a new SignalingServer with the given RoomHub.
func NewSignalingServer(hub *RoomHub) *SignalingServer {
	return &SignalingServer{hub: hub}
}

// Hub returns the room hub backing this signaling server.
func (s *SignalingServer) Hub() *RoomHub { return s.hub }

// HandleConn handles a single WebSocket connection for a video room.
// It blocks until the connection is closed. The peerID and name are assigned
// by the caller (typically from the URL or generated server-side).
//
// The conn parameter implements PeerConn, which is typically a *websocket.Conn
// but can be a mock for testing.
func (s *SignalingServer) HandleConn(groupKey, peerID, name string, conn PeerConn) {
	room := s.hub.GetRoom(groupKey)

	peer := &Peer{
		ID:       peerID,
		Name:     name,
		GroupKey: groupKey,
		conn:     conn,
	}

	// Check if room is full before adding
	if room.PeerCount() >= MaxMeshParticipants {
		_ = conn.WriteJSON(SignalMessage{
			Type:  "error",
			To:    peerID,
			Name:  "Room is full",
		})
		_ = conn.Close()
		return
	}

	if !room.AddPeer(peer) {
		_ = conn.WriteJSON(SignalMessage{
			Type:  "error",
			To:    peerID,
			Name:  "Room is full",
		})
		_ = conn.Close()
		return
	}

	log.Printf("video: peer %s (%s) joined room %s (%d peers)", peerID, name, groupKey, room.PeerCount())

	// Notify existing peers that a new peer joined
	s.broadcastJoin(room, peer)

	// Send current peer list to the new peer
	s.sendPeerList(room, peer)

	// Read loop
	defer func() {
		room.RemovePeer(peerID)
		s.broadcastLeave(room, peerID)
		s.hub.RemoveRoomIfEmpty(groupKey)
		_ = conn.Close()
		log.Printf("video: peer %s left room %s (%d peers)", peerID, groupKey, room.PeerCount())
	}()

	for {
		var msg SignalMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}
		s.handleMessage(room, peer, &msg)
	}
}

// handleMessage processes a single signaling message from a peer.
func (s *SignalingServer) handleMessage(room *Room, peer *Peer, msg *SignalMessage) {
	msg.From = peer.ID

	switch msg.Type {
	case "offer", "answer":
		// Relay SDP to the target peer
		target := room.GetPeer(msg.To)
		if target != nil {
			_ = target.conn.WriteJSON(msg)
		}

	case "ice":
		// Relay ICE candidate to the target peer
		target := room.GetPeer(msg.To)
		if target != nil {
			_ = target.conn.WriteJSON(msg)
		}

	case "mute_all":
		// Only broadcast to other peers — leader-only is enforced at a higher layer
		for _, p := range room.Peers() {
			if p.ID != peer.ID {
				_ = p.conn.WriteJSON(SignalMessage{
					Type: "mute_all",
					From: peer.ID,
					Name: peer.Name,
				})
			}
		}

	default:
		// Unknown message type — ignore
	}
}

// broadcastJoin notifies all existing peers that a new peer joined.
func (s *SignalingServer) broadcastJoin(room *Room, newPeer *Peer) {
	for _, p := range room.Peers() {
		if p.ID == newPeer.ID {
			continue
		}
		_ = p.conn.WriteJSON(SignalMessage{
			Type: "join",
			From: newPeer.ID,
			Name: newPeer.Name,
		})
	}
}

// broadcastLeave notifies all peers that a peer left.
func (s *SignalingServer) broadcastLeave(room *Room, peerID string) {
	for _, p := range room.Peers() {
		_ = p.conn.WriteJSON(SignalMessage{
			Type: "leave",
			From: peerID,
		})
	}
}

// sendPeerList sends the current list of peers to a specific peer.
func (s *SignalingServer) sendPeerList(room *Room, peer *Peer) {
	var peers []PeerInfo
	for _, p := range room.Peers() {
		if p.ID == peer.ID {
			continue
		}
		peers = append(peers, PeerInfo{
			ID:   p.ID,
			Name: p.Name,
		})
	}
	_ = peer.conn.WriteJSON(SignalMessage{
		Type:  "peers",
		To:    peer.ID,
		Peers: peers,
	})
}