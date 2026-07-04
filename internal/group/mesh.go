// SPDX-License-Identifier: AGPL-3.0
//
// WireGuard mesh peer registry (protocol §5.4.7 / G2).
//
// The mesh is a private overlay. A peer can decrypt traffic to /
// from the federation only if it holds a wg private key whose
// public half is in the mesh peer set. The peer set is a state-machine
// concept: ADD_HOST_PEER and REMOVE_HOST_PEER transitions add /
// remove peers, signed by the steward threshold AND a current peer
// co-signer.
//
// This module is the data structure that State.Apply uses to track
// the current peer set. Truth source: the state log. The live set is
// reconstructed by walking from genesis.
//
// G2 attack surface: without this gate, a compromised bootstrap
// (the operator who initially configures the mesh) could add ANY wg
// key to the mesh. With this gate, adding a peer requires M-of-N
// steward signatures (the steward set is itself gated by G3 custody
// tiers) AND a co-signature from an existing mesh member. Three
// independent keys / roles must agree.

package group

import (
	"sync"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
)

// MeshPeer is one entry in the mesh peer set: a wg public key + a
// mesh IP + a CoSigner pubkey. The peer is identified by the wg key.
type MeshPeer struct {
	// HostWGKey is the peer's WireGuard X25519 public key (32 bytes).
	HostWGKey *pb.PublicKey
	// MeshIP is the peer's static private IP in the overlay space.
	// IPv4 (4 bytes) or IPv6 (16 bytes).
	MeshIP []byte
	// CoSignerKey is the peer's dedicated Ed25519 CoSigner public
	// key (32 bytes). Used to verify ADD_HOST_PEER cosignatures.
	// Distinct from HostWGKey because X25519 outputs do not
	// coincide with Ed25519 points (cycle 51). May be empty for
	// bootstrap peers that pre-date the CoSigner convention; in
	// that case the peer cannot cosign ADD_HOST_PEER.
	CoSignerKey *pb.PublicKey
}

// PeerID returns a fixed-size key suitable for use as a map key.
// Two peers with the same wg key are the same peer regardless of IP.
func (p *MeshPeer) PeerID() ([32]byte, bool) {
	raw := p.HostWGKey.GetRaw()
	if len(raw) != 32 {
		return [32]byte{}, false
	}
	var k [32]byte
	copy(k[:], raw)
	return k, true
}

// meshPeerRegistry is the per-State view of the current mesh. Tracks
// the live set and the history of add/remove operations.
type meshPeerRegistry struct {
	mu         sync.Mutex
	peers      map[[32]byte]*MeshPeer // keyed by wg key
	byIP       map[string]*MeshPeer   // keyed by canonical mesh IP string
	byCoSigner map[[32]byte]*MeshPeer // keyed by CoSigner Ed25519 pubkey (cycle 56)
	addLog     []MeshPeer             // ordered list of additions
	removeLog  []*MeshPeer            // ordered list of removals
}

// newMeshPeerRegistry returns an empty registry.
func newMeshPeerRegistry() *meshPeerRegistry {
	return &meshPeerRegistry{
		peers:      make(map[[32]byte]*MeshPeer),
		byIP:       make(map[string]*MeshPeer),
		byCoSigner: make(map[[32]byte]*MeshPeer),
	}
}

// add inserts a peer. Returns ErrDuplicateMeshPeer if the wg key or
// mesh IP is already registered.
func (r *meshPeerRegistry) add(p *MeshPeer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := p.PeerID()
	if !ok {
		return ErrInvalidMeshPeer
	}
	if _, exists := r.peers[id]; exists {
		return ErrDuplicateMeshPeer
	}
	ipKey := string(p.MeshIP)
	if _, exists := r.byIP[ipKey]; exists {
		return ErrDuplicateMeshIP
	}
	r.peers[id] = p
	r.byIP[ipKey] = p
	if ck := p.CoSignerKey.GetRaw(); len(ck) == 32 {
		var key [32]byte
		copy(key[:], ck)
		r.byCoSigner[key] = p
	}
	r.addLog = append(r.addLog, *p)
	return nil
}

// remove deletes a peer by wg key. Returns ErrUnknownMeshPeer if
// the key isn't in the set.
func (r *meshPeerRegistry) remove(p *MeshPeer) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := p.PeerID()
	if !ok {
		return ErrInvalidMeshPeer
	}
	if _, exists := r.peers[id]; !exists {
		return ErrUnknownMeshPeer
	}
	delete(r.peers, id)
	delete(r.byIP, string(p.MeshIP))
	if ck := p.CoSignerKey.GetRaw(); len(ck) == 32 {
		var key [32]byte
		copy(key[:], ck)
		delete(r.byCoSigner, key)
	}
	r.removeLog = append(r.removeLog, p)
	return nil
}

// IsMember returns true if the given wg key is a current mesh peer.
func (r *meshPeerRegistry) IsMember(wgKey [32]byte) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.peers[wgKey]
	return ok
}

// Len returns the current peer count.
func (r *meshPeerRegistry) Len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers)
}

// Snapshot returns the current peer set (read-only copy).
func (r *meshPeerRegistry) Snapshot() []*MeshPeer {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*MeshPeer, 0, len(r.peers))
	for _, p := range r.peers {
		cp := *p
		out = append(out, &cp)
	}
	return out
}

// MeshPeers returns the current peer set for the state. Public API.
func (s *State) MeshPeers() []*MeshPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meshPeers == nil {
		return nil
	}
	return s.meshPeers.Snapshot()
}

// Branches returns BranchInfo for every branch in the group's
// forest. Sorted by branch ID ascending. Implements the BRANCH_LIST
// read query.
func (s *State) Branches() []*pb.BranchInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.branches.list()
}

// Branch returns the branch with the given ID, or nil if it
// doesn't exist on this host.
func (s *State) Branch(id BranchID) *Branch {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.branches.get(id)
}

// BranchCount returns the number of branches in the group's forest.
func (s *State) BranchCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.branches == nil {
		return 0
	}
	s.branches.mu.Lock()
	defer s.branches.mu.Unlock()
	return len(s.branches.branches)
}

// IsMeshMember returns true if a peer with the given wg key is a
// current mesh member of this group's mesh. Used by ADD_HOST_PEER to
// validate the co-signer's existing membership.
//
// This is the lock-acquiring variant — use IsMeshMemberLocked when
// already holding s.mu.
func (s *State) IsMeshMember(wgKey [32]byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meshPeers == nil {
		return false
	}
	return s.meshPeers.IsMember(wgKey)
}

// IsMeshMemberLocked is the lock-free variant for callers that
// already hold s.mu. Returns false if no mesh has been initialized.
func (s *State) IsMeshMemberLocked(wgKey [32]byte) bool {
	if s.meshPeers == nil {
		return false
	}
	return s.meshPeers.IsMember(wgKey)
}

// MeshPeerByCoSigner returns the mesh peer whose CoSignerKey matches
// the given Ed25519 pubkey, if any. Used to validate the cosigner of
// an ADD_HOST_PEER transition (cycle 56). Returns nil if no match.
func (s *State) MeshPeerByCoSigner(cosignerKey [32]byte) *MeshPeer {
	if s.meshPeers == nil {
		return nil
	}
	return s.meshPeers.byCoSigner[cosignerKey]
}

// addMeshPeerLocked inserts a peer into the state's registry. Called
// from State.Apply under the state lock when ADD_HOST_PEER succeeds.
func (s *State) addMeshPeerLocked(p *MeshPeer) error {
	if s.meshPeers == nil {
		s.meshPeers = newMeshPeerRegistry()
	}
	return s.meshPeers.add(p)
}

// removeMeshPeerLocked deletes a peer from the state's registry.
func (s *State) removeMeshPeerLocked(p *MeshPeer) error {
	if s.meshPeers == nil {
		return ErrUnknownMeshPeer
	}
	return s.meshPeers.remove(p)
}

// Errors emitted by the mesh peer registry. Surfaced to the caller
// (Apply) so the transition can be rejected with a clear error.
var (
	ErrDuplicateMeshPeer = &groupError{Kind: "duplicate_mesh_peer", Msg: "peer with this wg key already in the mesh"}
	ErrDuplicateMeshIP   = &groupError{Kind: "duplicate_mesh_ip", Msg: "mesh IP already assigned to another peer"}
	ErrInvalidMeshPeer   = &groupError{Kind: "invalid_mesh_peer", Msg: "mesh peer must have a 32-byte wg public key"}
	ErrUnknownMeshPeer   = &groupError{Kind: "unknown_mesh_peer", Msg: "no peer with this wg key in the mesh"}
)

// groupError is a structured error from the group package. Distinct
// from the generic fmt.Errorf paths so callers can switch on Kind
// without string matching.
type groupError struct {
	Kind string
	Msg  string
}

func (e *groupError) Error() string { return e.Kind + ": " + e.Msg }