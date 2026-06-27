// SPDX-License-Identifier: MIT
//
// Package wg implements the server-to-server WireGuard mesh transport.
//
// This is the private federation transport described in docs/02-PROTOCOL.md
// section 5.0. It is NOT the public client-facing surface; clients speak
// ConnectRPC over HTTPS.
//
// Design:
//   - Each host gets one wg device, configured with the public keys of every
//     other host in the mesh.
//   - The mesh IP space (e.g. 10.42.0.0/16) is private to the federation.
//   - Each wg device's outbound UDP packets are pushed through sim.Mesh (in
//     tests) or a real UDP socket (in production). The mesh is just an
//     impairment layer; encryption is real WireGuard end-to-end.
//   - This package owns the wg device lifecycle and the Bind abstraction; the
//     simulator (or the production wire) wires packets to peers via the
//     Sender and Receiver callbacks.

package wg

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/device"
)

// PeerID identifies a host in the federation.
type PeerID string

// Sender pushes a WireGuard transport packet onto the wire.
type Sender func(from PeerID, to PeerID, toAddr netip.Addr, payload []byte) error

// Mesh is the federation mesh. It owns one Peer per host.
type Mesh struct {
	mu sync.Mutex

	peers  map[PeerID]*Peer
	byAddr map[netip.Addr]PeerID
	// byKernelAddr maps "127.0.0.1:PORT" -> peer ID. simBind.ParseEndpoint
	// is given the kernel UDP address (since wireguard-go sees the real
	// socket we opened), so we need to resolve by that, not by mesh IP.
	byKernelAddr map[string]PeerID
	mtu          int

	sender Sender

	// peerBinds maps each peer to its simBind, so the receive loop knows
	// where to inject incoming packets. Set by StartPeer.
	peerBinds map[PeerID]*simBind

	// bridgeRunning and bridgeStop track the bridge goroutines started
	// by RunBridge. Idempotent.
	bridgeRunning bool
	bridgeStop    chan struct{}

	// outQueue holds packets pushed by sender goroutines. Drain flushes
	// it into sim.Mesh deterministically (under the test's clock).
	outQueue []queuedPacket

	closed chan struct{}
}

// Peer is one host's view of its wg configuration.
type Peer struct {
	ID       PeerID
	MeshAddr netip.Addr

	// PublicKey is the peer's wg public key (32 bytes).
	PublicKey []byte
	// PrivateKey is this peer's wg private key (32 bytes).
	PrivateKey []byte
	// PresharedKey is the optional pre-shared key (32 bytes). v1: nil.
	PresharedKey []byte

	// AllowedIPs for THIS peer (i.e. what IPs we accept from this peer).
	// For a federation mesh: the peer's single mesh-private IP /32.
	AllowedIPs []netip.Prefix

	// PersistentKeepalive interval in seconds. 0 = off.
	PersistentKeepalive int

	// Device is the wireguard device for this peer. Set by Start.
	Device *WireGuardDevice
}

// WireGuardDevice is a thin wrapper around the wireguard-go device.
type WireGuardDevice struct {
	peerID   PeerID
	meshAddr netip.Addr
	private  []byte

	Device *device.Device // exported so tests can call IpcGet/Up/Close
	bind   *simBind
	closed chan struct{}

	closeOnce sync.Once
}

// Device returns the underlying wireguard-go device. Callers can use
// IpcGet to inspect peer state (e.g. last_handshake_time_sec) and
// IpcSet to update config dynamically.
func (d *WireGuardDevice) WgDevice() *device.Device { return d.Device }

// Close shuts down the wg device. Safe to call multiple times.
func (d *WireGuardDevice) Close() error {
	d.closeOnce.Do(func() {
		if d.Device != nil {
			d.Device.Close()
		}
		close(d.closed)
	})
	return nil
}

// MeshLock returns the mesh's mutex. Exported for tests that need to
// safely read peer state. Production code should not need this.
func (m *Mesh) MeshLock() *sync.Mutex { return &m.mu }

// NewMesh creates a new mesh. Add peers via AddPeer; start them via
// StartPeer. The Sender callback wires the mesh to the wire.
func NewMesh(mtu int, sender Sender) *Mesh {
	if mtu <= 0 {
		mtu = 1420
	}
	return &Mesh{
		peers:        make(map[PeerID]*Peer),
		byAddr:       make(map[netip.Addr]PeerID),
		byKernelAddr: make(map[string]PeerID),
		mtu:          mtu,
		sender:       sender,
		peerBinds:    make(map[PeerID]*simBind),
		closed:       make(chan struct{}),
	}
}

// AddPeer registers a peer in the mesh. Returns an error if the ID is
// already present.
func (m *Mesh) AddPeer(p *Peer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.peers[p.ID]; exists {
		return fmt.Errorf("wg: peer %s already in mesh", p.ID)
	}
	m.peers[p.ID] = p
	m.byAddr[p.MeshAddr] = p.ID
	return nil
}

// Peers returns all peers (read-only snapshot).
func (m *Mesh) Peers() []*Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Peer, 0, len(m.peers))
	for _, p := range m.peers {
		out = append(out, p)
	}
	return out
}

// PeerByID returns the peer with the given ID, or nil.
func (m *Mesh) PeerByID(id PeerID) *Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.peers[id]
}

// PeerByAddr returns the peer with the given mesh-private IP, or nil.
func (m *Mesh) PeerByAddr(addr netip.Addr) *Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byAddr[addr]; ok {
		return m.peers[id]
	}
	return nil
}

// Resolve implements MeshResolver — looks up a peer by either its
// mesh-private IP (e.g. "10.42.0.2") or its kernel UDP address
// (e.g. "127.0.0.1:54321"). The kernel address is what wireguard-go's
// UAPI config passes as `endpoint=`. The mesh address is what the
// application layer uses to address packets.
func (m *Mesh) Resolve(s string) (PeerID, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok := m.byKernelAddr[s]; ok {
		return id, true
	}
	if addr, err := netip.ParseAddr(s); err == nil {
		if id, ok := m.byAddr[addr]; ok {
			return id, true
		}
	}
	return "", false
}

// MeshAddrForPeer derives a unique mesh-private IP for a peer ID.
// v1: hash-based. Production: directory-assigned.
func MeshAddrForPeer(id PeerID) netip.Addr {
	var sum [4]byte
	binary.LittleEndian.PutUint32(sum[:], hashString(string(id)))
	addr := netip.AddrFrom4([4]byte{10, 42, sum[0], sum[1]})
	return addr
}

func hashString(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// Close shuts down all peers.
func (m *Mesh) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	select {
	case <-m.closed:
		return nil
	default:
		close(m.closed)
	}
	for _, p := range m.peers {
		if p.Device != nil {
			p.Device.Close()
		}
	}
	for _, b := range m.peerBinds {
		if b != nil {
			b.Shutdown()
		}
	}
	return nil
}

// Send pushes a packet to the wire (encrypted). The wire layer is
// responsible for delivering it to the destination peer's wg device.
func (m *Mesh) Send(from PeerID, to PeerID, toAddr netip.Addr, payload []byte) error {
	if m.sender == nil {
		return fmt.Errorf("wg: no sender configured")
	}
	return m.sender(from, to, toAddr, payload)
}

// SetSender replaces the wire sender. Used when the wire has a circular
// dependency on the mesh (e.g. test wiring where the wire dispatches back
// into the mesh on receive).
func (m *Mesh) SetSender(s Sender) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sender = s
}

// Deliver injects an inbound packet (encrypted WireGuard transport) into
// the destination peer's simBind. The `from` argument is the peer ID of
// the original sender — wg-go needs this so its response packets are
// addressed to the correct peer (not back to itself).
func (m *Mesh) Deliver(to PeerID, payload []byte) error {
	return m.DeliverFrom(to, "", payload)
}

// DeliverFrom injects an inbound packet with a specific source peer.
// `from` is the original sender's peer ID (from sim.Message.From).
// If `from` is empty, the source is recorded as "unknown" and wg may
// not be able to send a reply (acceptable for one-way messages).
func (m *Mesh) DeliverFrom(to PeerID, from PeerID, payload []byte) error {
	m.mu.Lock()
	bind, ok := m.peerBinds[to]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("wg: no bind for peer %s", to)
	}
	var srcPeer PeerID = from
	if srcPeer == "" {
		srcPeer = to
	}
	bind.inject(payload, &simEndpoint{peerID: srcPeer, meshAddr: netip.IPv4Unspecified()})
	return nil
}

// registerBind associates a peer with its bind (called by StartPeer).
func (m *Mesh) registerBind(p PeerID, b *simBind) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peerBinds[p] = b
}