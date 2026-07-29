// SPDX-License-Identifier: AGPL-3.0
package video

import "errors"

// ErrSFUNotImplemented is returned by the stub SFU provider.
var ErrSFUNotImplemented = errors.New("SFU mode not yet implemented — large group video calls coming soon")

// SFUProvider is the interface boundary for a future SFU (Selective Forwarding
// Unit) implementation. When the number of participants exceeds
// MaxMeshParticipants (8), the system should use an SFU instead of mesh P2P.
//
// v1 does NOT implement this interface. The video room template checks the
// participant count and shows "Large group video calls coming soon" if
// it exceeds MaxMeshParticipants.
//
// Future implementation plan (Tier 2 — SFU mode):
//
//   1. The Go server creates a Pion WebRTC PeerConnection for each participant.
//   2. Each participant sends one upstream track (audio + video) to the server.
//   3. The server forwards (forwards, not transcodes) each track to all other
//      participants as downstream tracks.
//   4. The server acts as a relay — it does not encode/decode media.
//   5. Bandwidth: each participant sends 1 stream up, receives N-1 streams down.
//      Total server bandwidth: O(N) rather than O(N²) for mesh.
//   6. The signaling server adds a "sfu_offer" message type that contains the
//      server's SDP offer for the joining peer.
//
// Pion makes this possible because github.com/pion/webrtc provides a full
// WebRTC implementation in Go — the server can create RTCPeerConnections,
// receive tracks, and forward them.
//
// Key types for future SFU implementation:
//   - webrtc.PeerConnection (server-side, one per participant)
//   - webrtc.TrackLocalStaticRTP / webrtc.TrackRemote
//   - Interceptors for bandwidth estimation and congestion control
//
// See: https://github.com/pion/webrtc/tree/main/examples/sfu
type SFUProvider interface {
	// JoinRoom adds a participant to the SFU session for a room.
	// Returns an SDP offer from the server that the browser should answer.
	JoinRoom(groupKey, peerID, name string) (sdpOffer string, err error)

	// HandleAnswer processes the browser's SDP answer to the server's offer.
	HandleAnswer(peerID string, sdpAnswer string) error

	// AddRemoteICE processes an ICE candidate from the browser.
	AddRemoteICE(peerID, candidate, label string) error

	// LeaveRoom removes a participant from the SFU session.
	LeaveRoom(peerID string) error

	// ParticipantCount returns the number of active participants.
	ParticipantCount(groupKey string) int
}

// sfuUnavailable is a stub that returns "not implemented" for all operations.
// It exists to make the interface boundary explicit and testable.
// In v1, the video room template checks participant count client-side and
// shows "Large group video calls coming soon" instead of calling this.
type sfuUnavailable struct{}

// SFUUnavailable is the default (unimplemented) SFU provider.
var SFUUnavailable SFUProvider = sfuUnavailable{}

func (sfuUnavailable) JoinRoom(groupKey, peerID, name string) (string, error) {
	return "", ErrSFUNotImplemented
}

func (sfuUnavailable) HandleAnswer(peerID, sdpAnswer string) error {
	return ErrSFUNotImplemented
}

func (sfuUnavailable) AddRemoteICE(peerID, candidate, label string) error {
	return ErrSFUNotImplemented
}

func (sfuUnavailable) LeaveRoom(peerID string) error {
	return ErrSFUNotImplemented
}

func (sfuUnavailable) ParticipantCount(groupKey string) int {
	return 0
}