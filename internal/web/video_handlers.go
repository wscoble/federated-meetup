// SPDX-License-Identifier: AGPL-3.0
package web

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/wscoble/federated-meetup/internal/video"
)

// videoRoomData is the template data for video_room.html.
type videoRoomData struct {
	pageBase
	EventTitle         string
	GroupKey            string
	EventID             string
	GroupName           string
	PeerID             string
	PeerName            string
	WSUrl              string
	IsLeader           bool
	TooManyParticipants bool
}

// getVideoSig returns the SignalingServer, creating it on first use.
func (s *Server) getVideoSig() *video.SignalingServer {
	if s.videoSig == nil {
		s.videoSig = video.NewSignalingServer(s.videoHub)
	}
	return s.videoSig
}

// handleVideoRoom renders the video room page.
// Accessible when the event's video room is open (15 min before to 2 hours after start).
func (s *Server) handleVideoRoom(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	eventID := r.PathValue("event_id")
	if groupKey == "" || eventID == "" {
		http.Error(w, "group_key and event_id required", http.StatusBadRequest)
		return
	}

	// Get the event to check if room is open
	event, err := s.eventFromProduct(groupKey, eventID)
	if err != nil {
		s.renderNotFound(w, r)
		return
	}

	// Check if room is open
	now := s.now()
	startsAt := time.Unix(event.StartsAt, 0)
	if !video.IsRoomOpen(now, startsAt) {
		s.renderPage(w, "error", pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		})
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// Get group name
	groupName := groupKey
	if g, err := s.groupFromProduct(groupKey); err == nil && g.DisplayName != "" {
		groupName = g.DisplayName
	}

	// Generate peer ID
	peerID := generatePeerID()

	// Get name from query param or default
	peerName := r.URL.Query().Get("name")
	if peerName == "" {
		peerName = "Guest"
	}

	// Check if user is the leader (organizer)
	_, isLeader := s.isAuthenticated(r)

	// Check current participant count
	participantCount := s.videoHub.ParticipantCount(groupKey)
	tooMany := participantCount >= video.MaxMeshParticipants

	// Build WebSocket URL
	wsScheme := "ws"
	if r.TLS != nil {
		wsScheme = "wss"
	}
	wsUrl := fmt.Sprintf("%s://%s/ws/video/%s?name=%s", wsScheme, r.Host, groupKey, peerName)

	s.renderPage(w, "video_room", videoRoomData{
		pageBase: pageBase{
			CSRFToken: csrfTokenFromRequest(r),
		},
		EventTitle:         event.Title,
		GroupKey:           groupKey,
		EventID:            eventID,
		GroupName:          groupName,
		PeerID:             peerID,
		PeerName:           peerName,
		WSUrl:              wsUrl,
		IsLeader:           isLeader,
		TooManyParticipants: tooMany,
	})
}

// handleVideoWS handles the WebSocket endpoint for video signaling.
func (s *Server) handleVideoWS(w http.ResponseWriter, r *http.Request) {
	groupKey := r.PathValue("group_key")
	if groupKey == "" {
		http.Error(w, "group_key required", http.StatusBadRequest)
		return
	}

	// startsAtFor resolves a group_key to the event start time.
	// We look up events for this group and find the one that's currently
	// within the room-open window.
	startsAtFor := func(gk string) (int64, bool) {
		events, err := s.store.ListEventsByGroup(gk)
		if err != nil {
			return 0, false
		}
		now := s.now()
		for _, e := range events {
			startsAt := time.Unix(e.StartsAt, 0)
			if video.IsRoomOpen(now, startsAt) {
				return e.StartsAt, true
			}
		}
		return 0, false
	}

	handler := video.WSHandler(s.getVideoSig(), s.now, startsAtFor)
	handler(w, r)
}

// generatePeerID generates a random 16-char hex peer ID.
func generatePeerID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte("peer0000"))
	}
	return hex.EncodeToString(b)
}

// _ ensures log is used
var _ = log.Printf