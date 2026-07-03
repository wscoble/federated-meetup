// SPDX-License-Identifier: AGPL-3.0
//
// Package wg: sim-backed conn.Bind.
//
// Architecture (v3-final — kernel UDP socket + sim.Mesh bridge):
//
//   Each peer owns a real localhost UDP socket. Wireguard-go uses this
//   socket as if it were a normal L4 endpoint: it calls bind.Send to push
//   outbound encrypted datagrams, and bind.recvFunc to pull inbound
//   datagrams.
//
//   The bridge goroutines (transport/wg/bridge.go) sit between the socket
//   and sim.Mesh:
//     - Per-peer sender goroutine: drains bind.sendCh into mesh.outQueue.
//     - mesh.Drain(world): flushes outQueue into sim.Mesh, then pulls
//       delivered packets and injects them into the destination peer's bind.
//
//   In production the bridge is replaced by a real network; sim.Mesh is
//   replaced by the actual Internet. The Bind interface is identical.

package wg

import (
	"fmt"
	"net"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
)

// simBind is a conn.Bind backed by a real localhost UDP socket whose
// packets are routed through sim.Mesh by an external bridge (see bridge.go).
//
// Lifecycle (v3-final):
//
//   - newSimBind opens the UDP socket once and keeps it open forever (or
//     until Shutdown).
//   - Each Open() returns a recvFunc bound to a per-cycle done channel.
//     wg-go spawns the recvFunc in a goroutine and tracks it via
//     device.net.stopping — when we return, that wg is decremented.
//   - Each Close() closes the current cycle's done channel so wg-go can
//     reap the goroutine. The bind stays usable for the next Open.
//   - Shutdown() is the final teardown — closes the UDP socket and refuses
//     future Open calls.
type simBind struct {
	peerID PeerID
	addr   netip.Addr

	// LocalAddr is the UDP socket wireguard-go writes to.
	conn *net.UDPConn

	// boundPort is the actual port the socket is bound to.
	boundPort uint16

	// MeshAddr is this peer's private mesh IP.
	meshAddr netip.Addr

	mu       sync.Mutex
	shutdown bool // true after Shutdown() — no more Opens allowed
	done     chan struct{} // current Open cycle's done channel
	signalCh chan struct{} // signals recvFunc that recvQ has data
	resolver MeshResolver
	sendCh   chan outgoingPacket
	recvQ    []recvEntry
}

// simEndpoint is the destination of a bind.Send.
type simEndpoint struct {
	meshAddr netip.Addr
	peerID   PeerID
}

func (e *simEndpoint) ClearSrc()           {}
func (e *simEndpoint) SrcToString() string { return "" }
func (e *simEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }
func (e *simEndpoint) DstToString() string { return string(e.peerID) }
func (e *simEndpoint) DstToBytes() []byte  { return []byte(e.peerID) }
func (e *simEndpoint) DstIP() netip.Addr   { return e.meshAddr }

// newSimBind allocates a UDP socket on the loopback at an ephemeral
// port and returns a Bind ready for Open(). The bridge (see bridge.go)
// wires this socket to sim.Mesh.
func newSimBind(peerID PeerID, meshAddr netip.Addr) (*simBind, error) {
	udpAddr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("wg: resolve udp addr: %w", err)
	}
	c, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("wg: listen udp for %s: %w", peerID, err)
	}
	port := uint16(c.LocalAddr().(*net.UDPAddr).Port)
	return &simBind{
		peerID:    peerID,
		addr:      netip.AddrFrom4([4]byte{127, 0, 0, 1}),
		conn:      c,
		boundPort: port,
		meshAddr:  meshAddr,
	}, nil
}

// Open implements conn.Bind. Returns a recvFunc bound to a per-cycle done
// channel. wg-go calls this after a Close on every listen_port change.
func (b *simBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shutdown {
		return nil, 0, netErrClosed
	}
	done := make(chan struct{})
	b.done = done
	return []conn.ReceiveFunc{func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		return b.recvFunc(bufs, sizes, eps, done)
	}}, b.boundPort, nil
}

// recvFunc reads inbound packets from recvQ. The cycleDone channel is the
// done channel for THIS open cycle — passed in as a parameter so that
// concurrent Open cycles don't see each other's done.
func (b *simBind) recvFunc(bufs [][]byte, sizes []int, eps []conn.Endpoint, cycleDone chan struct{}) (int, error) {
	select {
	case <-cycleDone:
		return 0, netErrClosed
	case <-b.signalCh:
	}

	if len(bufs) == 0 {
		return 0, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.recvQ) == 0 {
		return 0, nil
	}
	pkt := b.recvQ[0]
	b.recvQ = b.recvQ[1:]
	if len(pkt.data) > len(bufs[0]) {
		return 0, fmt.Errorf("wg: packet too large for buffer: %d > %d", len(pkt.data), len(bufs[0]))
	}
	copy(bufs[0], pkt.data)
	sizes[0] = len(pkt.data)
	eps[0] = pkt.from
	return 1, nil
}

// inject pushes a packet into the receive queue. Called by the bridge
// after sim.Mesh delivers a packet for this peer.
func (b *simBind) inject(payload []byte, from *simEndpoint) {
	b.mu.Lock()
	b.recvQ = append(b.recvQ, recvEntry{data: append([]byte(nil), payload...), from: from})
	signal := b.signalCh
	b.mu.Unlock()
	select {
	case signal <- struct{}{}:
	default:
	}
}

// Send implements conn.Bind. Wireguard-go calls this with an encrypted
// UDP datagram. We push it onto the outgoing channel; the bridge forwards
// it into sim.Mesh.
func (b *simBind) Send(bufs [][]byte, endpoint conn.Endpoint) error {
	dst, ok := endpoint.(*simEndpoint)
	if !ok {
		return netErrInvalidEndpoint
	}
	for _, buf := range bufs {
		pkt := append([]byte(nil), buf...)
		select {
		case b.sendCh <- outgoingPacket{to: PeerID(dst.peerID), data: pkt}:
		case <-b.done:
			return netErrClosed
		}
	}
	return nil
}

// ParseEndpoint implements conn.Bind. Looks up the destination peer by
// either mesh IP or kernel UDP address via the registered resolver.
func (b *simBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	id, ok := b.resolver.Resolve(s)
	if !ok {
		return nil, netErrUnknownPeer
	}
	return &simEndpoint{meshAddr: netip.IPv4Unspecified(), peerID: id}, nil
}

// BatchSize implements conn.Bind. Loopback has no batch I/O.
func (b *simBind) BatchSize() int { return 1 }

// SetMark implements conn.Bind. No-op in sim.
func (b *simBind) SetMark(uint32) error { return nil }

// Close implements conn.Bind. Unblocks the currently-running recvFunc by
// closing the current cycle's done channel so wg-go's stopping wg can
// decrement. Idempotent.
func (b *simBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shutdown {
		return nil
	}
	if b.done != nil {
		select {
		case <-b.done:
		default:
			close(b.done)
		}
	}
	return nil
}

// Shutdown is the final teardown. Called by the user (not by wireguard-go)
// when the bind will never be used again.
func (b *simBind) Shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shutdown {
		return
	}
	b.shutdown = true
	if b.done != nil {
		select {
		case <-b.done:
		default:
			close(b.done)
		}
	}
	if b.conn != nil {
		b.conn.Close()
	}
	if b.sendCh != nil {
		close(b.sendCh)
	}
}

// LocalAddr returns the bound UDP address (kernel-assigned).
func (b *simBind) LocalAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: int(b.boundPort)}
}

// ----------------------------------------------------------------------------
// Internal plumbing (not part of conn.Bind)
// ----------------------------------------------------------------------------

type recvEntry struct {
	data []byte
	from *simEndpoint
}

// outgoingPacket is what simBind.Send puts on its sendCh. The destination
// is a PeerID (already resolved by ParseEndpoint).
type outgoingPacket struct {
	to   PeerID
	data []byte
}

// MeshResolver maps a peer identifier to a peer ID. The identifier may be
// either a mesh-private IP (e.g. "10.42.0.2") or a kernel UDP address
// (e.g. "127.0.0.1:54321"). The bridge registers both forms for every peer.
type MeshResolver interface {
	Resolve(string) (PeerID, bool)
}

func (b *simBind) initPlumbing(resolver MeshResolver) {
	b.resolver = resolver
	b.sendCh = make(chan outgoingPacket, 64)
	b.signalCh = make(chan struct{}, 1)
	b.done = make(chan struct{})
	b.recvQ = make([]recvEntry, 0, 16)
}

// ----------------------------------------------------------------------------
// Errors
// ----------------------------------------------------------------------------

type bindError string

func (e bindError) Error() string { return string(e) }

var (
	netErrClosed          = bindError("bind closed")
	netErrInvalidEndpoint = bindError("invalid endpoint type")
	netErrUnknownPeer     = bindError("unknown peer")
)