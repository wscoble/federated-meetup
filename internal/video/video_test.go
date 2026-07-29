// SPDX-License-Identifier: AGPL-3.0
package video

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// mockConn is a mock WebSocket connection for testing the signaling relay.
type mockConn struct {
	mu       sync.Mutex
	writeMu  sync.Mutex
	writes   [][]byte // JSON messages written to this conn
	readCh   chan SignalMessage
	closed   bool
	writeErr error
}

func newMockConn() *mockConn {
	return &mockConn{
		readCh: make(chan SignalMessage, 100),
	}
}

func (m *mockConn) WriteJSON(v interface{}) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	if m.writeErr != nil {
		return m.writeErr
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.writes = append(m.writes, b)
	m.mu.Unlock()
	return nil
}

func (m *mockConn) ReadJSON(v interface{}) error {
	msg, ok := <-m.readCh
	if !ok {
		return errConnClosed
	}
	// Serialize then deserialize into v
	b, _ := json.Marshal(msg)
	return json.Unmarshal(b, v)
}

func (m *mockConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed {
		m.closed = true
		close(m.readCh)
	}
	return nil
}

// send pushes a message into the read channel for the conn to "receive".
func (m *mockConn) send(msg SignalMessage) {
	m.readCh <- msg
}

// getWrites returns a copy of all written messages parsed as SignalMessage.
func (m *mockConn) getWrites() []SignalMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	var msgs []SignalMessage
	for _, b := range m.writes {
		var msg SignalMessage
		if json.Unmarshal(b, &msg) == nil {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

var errConnClosed = &mockError{"connection closed"}

type mockError struct{ s string }

func (e *mockError) Error() string { return e.s }

// ---- Signaling relay test ----

func TestSignalingRelay(t *testing.T) {
	hub := NewRoomHub()
	sig := NewSignalingServer(hub)

	// Create two mock connections (two peers)
	conn1 := newMockConn()
	conn2 := newMockConn()

	// Run HandleConn for peer1 in background
	go sig.HandleConn("test-room", "peer1", "Alice", conn1)

	// Give peer1 time to register
	time.Sleep(50 * time.Millisecond)

	// Connect peer2
	go sig.HandleConn("test-room", "peer2", "Bob", conn2)

	// Give peer2 time to register and receive peer list
	time.Sleep(50 * time.Millisecond)

	// Peer2 should have received a "peers" message listing peer1
	writes2 := conn2.getWrites()
	foundPeers := false
	for _, w := range writes2 {
		if w.Type == "peers" {
			foundPeers = true
			if len(w.Peers) != 1 {
				t.Errorf("expected 1 peer in peers list, got %d", len(w.Peers))
			}
			if len(w.Peers) > 0 && w.Peers[0].ID != "peer1" {
				t.Errorf("expected peer1 in peers list, got %s", w.Peers[0].ID)
			}
		}
		// Peer1 should have received a "join" message about peer2
	}
	if !foundPeers {
		t.Error("peer2 did not receive peers list")
	}

	// Peer1 should have received a "join" message about peer2
	writes1 := conn1.getWrites()
	foundJoin := false
	for _, w := range writes1 {
		if w.Type == "join" && w.From == "peer2" {
			foundJoin = true
			if w.Name != "Bob" {
				t.Errorf("expected join name 'Bob', got '%s'", w.Name)
			}
		}
	}
	if !foundJoin {
		t.Error("peer1 did not receive join notification for peer2")
	}

	// Now test SDP offer relay: peer1 sends an offer to peer2
	conn1.send(SignalMessage{
		Type: "offer",
		To:   "peer2",
		SDP:  "v=0\r\no=- 12345 1 IN IP4 0.0.0.0\r\n",
	})

	time.Sleep(50 * time.Millisecond)

	// Peer2 should have received the offer
	writes2 = conn2.getWrites()
	foundOffer := false
	for _, w := range writes2 {
		if w.Type == "offer" && w.From == "peer1" {
			foundOffer = true
			if w.SDP == "" {
				t.Error("offer SDP is empty")
			}
		}
	}
	if !foundOffer {
		t.Error("peer2 did not receive SDP offer from peer1")
	}

	// Test ICE candidate relay: peer2 sends ICE to peer1
	conn2.send(SignalMessage{
		Type: "ice",
		To:   "peer1",
		ICE:  "candidate:1 1 udp 2122252542 192.168.1.1 50000 typ host",
	})

	time.Sleep(50 * time.Millisecond)

	writes1 = conn1.getWrites()
	foundICE := false
	for _, w := range writes1 {
		if w.Type == "ice" && w.From == "peer2" {
			foundICE = true
			if w.ICE == "" {
				t.Error("ICE candidate is empty")
			}
		}
	}
	if !foundICE {
		t.Error("peer1 did not receive ICE candidate from peer2")
	}

	// Test leave: close peer2's connection
	conn2.Close()
	time.Sleep(50 * time.Millisecond)

	// Peer1 should have received a "leave" message
	writes1 = conn1.getWrites()
	foundLeave := false
	for _, w := range writes1 {
		if w.Type == "leave" && w.From == "peer2" {
			foundLeave = true
		}
	}
	if !foundLeave {
		t.Error("peer1 did not receive leave notification for peer2")
	}

	// Cleanup
	conn1.Close()
	time.Sleep(50 * time.Millisecond)
}

// ---- Room open/close logic test ----

func TestRoomOpenCloseLogic(t *testing.T) {
	startsAt := time.Date(2026, 7, 29, 19, 0, 0, 0, time.UTC) // 7:00 PM

	// Room opens 15 min before start (6:45 PM) and closes 2 hours after (9:00 PM)

	// Before open: 6:30 PM → closed
	before := time.Date(2026, 7, 29, 18, 30, 0, 0, time.UTC)
	if IsRoomOpen(before, startsAt) {
		t.Error("room should be closed 30 min before start")
	}

	// At open time: 6:45 PM → open
	atOpen := time.Date(2026, 7, 29, 18, 45, 0, 0, time.UTC)
	if !IsRoomOpen(atOpen, startsAt) {
		t.Error("room should be open at 15 min before start")
	}

	// During event: 7:30 PM → open
	during := time.Date(2026, 7, 29, 19, 30, 0, 0, time.UTC)
	if !IsRoomOpen(during, startsAt) {
		t.Error("room should be open during event")
	}

	// At close time: 9:00 PM → still open (inclusive)
	atClose := time.Date(2026, 7, 29, 21, 0, 0, 0, time.UTC)
	if !IsRoomOpen(atClose, startsAt) {
		t.Error("room should be open at 2 hours after start")
	}

	// After close: 9:01 PM → closed
	after := time.Date(2026, 7, 29, 21, 1, 0, 0, time.UTC)
	if IsRoomOpen(after, startsAt) {
		t.Error("room should be closed 2 hours + 1 min after start")
	}

	// Exactly at start time → open
	exactStart := time.Date(2026, 7, 29, 19, 0, 0, 0, time.UTC)
	if !IsRoomOpen(exactStart, startsAt) {
		t.Error("room should be open at exact start time")
	}
}

// ---- Participant limit test ----

func TestParticipantLimit(t *testing.T) {
	hub := NewRoomHub()
	room := hub.GetRoom("test-room")

	// Add MaxMeshParticipants peers
	for i := 0; i < MaxMeshParticipants; i++ {
		peer := &Peer{
			ID:   "peer" + string(rune('0'+i)),
			Name: "User" + string(rune('0'+i)),
		}
		if !room.AddPeer(peer) {
			t.Errorf("failed to add peer %d (room should accept up to %d)", i, MaxMeshParticipants)
		}
	}

	// Verify count
	if room.PeerCount() != MaxMeshParticipants {
		t.Errorf("expected %d peers, got %d", MaxMeshParticipants, room.PeerCount())
	}

	// Try to add one more — should fail
	overflowPeer := &Peer{
		ID:   "peer-overflow",
		Name: "Overflow",
	}
	if room.AddPeer(overflowPeer) {
		t.Error("should not be able to add more than MaxMeshParticipants peers")
	}

	// Remove one and verify we can add again
	room.RemovePeer("peer0")
	if room.PeerCount() != MaxMeshParticipants-1 {
		t.Errorf("expected %d peers after removal, got %d", MaxMeshParticipants-1, room.PeerCount())
	}

	if !room.AddPeer(overflowPeer) {
		t.Error("should be able to add a peer after removing one")
	}

	// Verify the hub's ParticipantCount matches
	hubCount := hub.ParticipantCount("test-room")
	if hubCount != MaxMeshParticipants {
		t.Errorf("hub participant count = %d, want %d", hubCount, MaxMeshParticipants)
	}
}

// ---- Test RoomHub cleanup ----

func TestRoomHubCleanup(t *testing.T) {
	hub := NewRoomHub()
	_ = hub.GetRoom("empty-room")

	// Room exists but has no peers
	if !hub.RoomExists("empty-room") {
		t.Error("room should exist")
	}

	// Should be removable when empty
	if !hub.RemoveRoomIfEmpty("empty-room") {
		t.Error("should remove empty room")
	}

	if hub.RoomExists("empty-room") {
		t.Error("room should not exist after cleanup")
	}

	// Add a peer, then try cleanup — should not remove
	room2 := hub.GetRoom("active-room")
	room2.AddPeer(&Peer{ID: "p1", Name: "P1"})
	if hub.RemoveRoomIfEmpty("active-room") {
		t.Error("should not remove room with active peers")
	}
}