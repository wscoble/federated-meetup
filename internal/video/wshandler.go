// SPDX-License-Identifier: AGPL-3.0
package video

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Allow all origins — this is behind Tailscale, not public internet.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// WSHandler returns an http.HandlerFunc that handles WebSocket connections
// for video rooms at /ws/video/{group_key}.
//
// The signaling parameter is the SignalingServer that relays messages between
// peers. The now parameter is a clock function for room-open checks.
// The startsAtFor function resolves a group_key to the event start time (unix
// seconds); if it returns ok=false, the room is treated as not found.
func WSHandler(sig *SignalingServer, now func() time.Time, startsAtFor func(groupKey string) (int64, bool)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		groupKey := r.PathValue("group_key")
		if groupKey == "" {
			http.Error(w, "group_key required", http.StatusBadRequest)
			return
		}

		// Decode URL-encoded group key
		if decoded, err := url.PathUnescape(groupKey); err == nil {
			groupKey = decoded
		}

		// Check if room is open
		startsAt, ok := startsAtFor(groupKey)
		if !ok {
			http.Error(w, "video room not found", http.StatusNotFound)
			return
		}

		// Check room open time window
		startTime := time.Unix(startsAt, 0)
		currentTime := now()
		if !IsRoomOpen(currentTime, startTime) {
			http.Error(w, "video room is not open yet", http.StatusForbidden)
			return
		}

		// Check participant count — if room is already at max for mesh, reject
		if sig.Hub().ParticipantCount(groupKey) >= MaxMeshParticipants {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				log.Printf("video: upgrade error: %v", err)
				return
			}
			_ = conn.WriteJSON(SignalMessage{
				Type: "error",
				Name: fmt.Sprintf("Room is full (max %d participants in mesh mode)", MaxMeshParticipants),
			})
			_ = conn.Close()
			return
		}

		// Generate peer ID
		peerID := generatePeerID()

		// Get name from query param or default
		name := r.URL.Query().Get("name")
		if name == "" {
			name = "Guest"
		}
		name = sanitizeName(name)

		// Upgrade to WebSocket
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("video: upgrade error: %v", err)
			return
		}

		// Handle the connection (blocks until closed)
		sig.HandleConn(groupKey, peerID, name, &wsConn{conn: conn})
	}
}

// wsConn wraps a *websocket.Conn to implement the PeerConn interface.
type wsConn struct {
	conn *websocket.Conn
}

func (w *wsConn) WriteJSON(v interface{}) error {
	return w.conn.WriteJSON(v)
}

func (w *wsConn) ReadJSON(v interface{}) error {
	return w.conn.ReadJSON(v)
}

func (w *wsConn) Close() error {
	return w.conn.Close()
}

// generatePeerID generates a random 16-char hex peer ID.
func generatePeerID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte("peer0000"))
	}
	return hex.EncodeToString(b)
}

// sanitizeName limits the name length and removes control characters.
func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if len(name) > 64 {
		name = name[:64]
	}
	// Remove control characters
	var b strings.Builder
	for _, r := range name {
		if r >= 32 && r != 127 {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if result == "" {
		return "Guest"
	}
	return result
}