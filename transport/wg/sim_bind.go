// SPDX-License-Identifier: MIT
//
// Package wg: sim-backed conn.Bind.
//
// Architecture (v3 — kernel UDP socket + sim.Mesh bridge):
//
//   Each peer owns a real localhost UDP socket. Wireguard-go uses this
//   socket as if it were a normal L4 endpoint: it calls bind.Send to push
//   outbound encrypted datagrams to the kernel, and bind.recvFunc to pull
//   inbound datagrams from the kernel.
//
//   The bridge goroutine (one per peer, started by StartPeer) sits between
//   the socket and sim.Mesh:
//
//     wg Send → bind.sendChan → bridge → sim.Mesh.Send
//     sim.Mesh delivers → bridge → kernel socket.Write → wg recvFunc
//
//   Why not just let the kernel route loopback UDP between peers directly?
//   Because we want every packet to traverse sim.Mesh (with the DDIL
//   profile applied — drop, latency, jitter, reorder, partition). The
//   bridge is the chokepoint that enforces this. Packets still go through
//   a real socket so wireguard-go's Bind lifecycle (Open/Close on
//   listen_port changes) works the way it's designed to.
//
//   In production, the bridge is replaced by a real network interface and
//   the sim.Mesh is replaced by the actual Internet. The Bind interface
//   is identical.

package wg

import (
	"fmt"
	"net"
	"net/netip"
	"sync"

	"golang.zx2c4.com/wireguard/conn"
)

// simBind is a conn.Bind backed by a real localhost UDP socket whose packets
// are routed through sim.Mesh by an external bridge (see startBridge).
//
// Lifecycle model (v3-final):
//
//   - newSimBind opens the UDP socket once and keeps it open forever
//     (or until Shutdown).
//   - Each Open() starts a fresh recvFunc goroutine that wg-go spawns.
//     We don't manage that goroutine; wg-go does via device.net.stopping.
//   - Each Close() unblocks the currently-running recvFunc by closing
//     the current done channel. The recvFunc returns; wg-go's stopping
//     wg is decremented; next Open can proceed.
//   - Shutdown() is the final teardown — closes the UDP socket and
//     blocks any future Open.
type simBind struct {
	peerID PeerID
	addr   netip.Addr

	// LocalAddr is the UDP socket wireguard-go writes to.
	conn *net.UDPConn

	// boundPort is the actual port the socket is bound to.
	boundPort uint16

	// MeshAddr is this peer's private mesh IP.
	meshAddr netip.Addr

	mu        sync.Mutex
	shutdown  bool // true after Shutdown() — no more Opens allowed
	done      chan struct{} // closed by current Close() to unblock recvFunc
	signalCh  chan struct{} // signals recvFunc that recvQ has data
	resolver  MeshResolver
	sendCh    chan outgoingPacket
	recvQ     []recvEntry
}

// simEndpoint is the destination of a bind.Send. It remembers which simBind
// to deliver to (via the bridge) and what mesh IP the packet claims.
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

// newSimBind allocates a UDP socket on the loopback at an ephemeral port
// and returns a Bind ready for Open(). The bridge (see startBridge) wires
// this socket to sim.Mesh.
func newSimBind(peerID PeerID, meshAddr netip.Addr) (*simBind, error) {
	// Bind to loopback at an ephemeral port.
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

// Open implements conn.Bind. wireguard-go calls this after a Close on every
// listen_port change. We return a recvFunc bound to a per-cycle done
// channel. If the bind was fully shut down (Shutdown), return closed-error.
func (b *simBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shutdown {
		return nil, 0, netErrClosed
	}
	// Fresh done channel for this Open cycle.
	done := make(chan struct{})
	b.done = done
	return []conn.ReceiveFunc{func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		return b.recvFunc(bufs, sizes, eps, done)
	}}, b.boundPort, nil
}

// recvFunc is what wireguard-go calls to pull inbound packets. We block
// on either a packet arriving (signalCh) or the current done channel
// closing. wg-go spawns this in a goroutine and tracks it via
// device.net.stopping — when we return, that wg is decremented.
// recvFunc reads inbound packets from recvQ. The `cycleDone` channel is
// the done channel for THIS open cycle — passed in as a parameter so that
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

// inject is called by the bridge to deliver a packet into our receive queue.
func (b *simBind) inject(data []byte, from *simEndpoint) {
	b.mu.Lock()
	b.recvQ = append(b.recvQ, recvEntry{data: append([]byte(nil), data...), from: from})
	signal := b.signalCh
	b.mu.Unlock()
	// Non-blocking signal — broadcast so a waiter wakes up.
	select {
	case signal <- struct{}{}:
	default:
	}
}

// Send implements conn.Bind. Wireguard-go calls this with an encrypted UDP
// datagram. We push it onto the outgoing channel; the bridge forwards it
// into sim.Mesh.
func (b *simBind) Send(bufs [][]byte, endpoint conn.Endpoint) error {
	dst, ok := endpoint.(*simEndpoint)
	if !ok {
		return netErrInvalidEndpoint
	}
	for _, buf := range bufs {
		// Copy so the caller can reuse its buffer.
		pkt := append([]byte(nil), buf...)
		select {
		case b.sendCh <- outgoingPacket{to: dst, data: pkt}:
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
	// We don't know the meshAddr here — the resolver can give us the
	// kernel addr mapping; the mesh IP is only used by DstIP() which the
	// device uses for routing. Since wireguard-go's allowed_ips uses the
	// peer-config-stored IP (from UAPI), DstIP is informational.
	return &simEndpoint{meshAddr: netip.IPv4Unspecified(), peerID: id}, nil
}

// BatchSize implements conn.Bind. Loopback has no batch I/O.
func (b *simBind) BatchSize() int { return 1 }

// SetMark implements conn.Bind. No-op in sim.
func (b *simBind) SetMark(uint32) error { return nil }

// Close implements conn.Bind. Unblocks the currently-running recvFunc by
// closing the current done channel so wg-go's stopping wg can decrement.
// Idempotent across multiple calls in the same Open cycle.
func (b *simBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.shutdown {
		return nil
	}
	if b.done != nil {
		select {
		case <-b.done:
			// Already closed.
		default:
			close(b.done)
		}
	}
	return nil
}

// Shutdown is the final teardown. Called by the user (not by wireguard-go)
// when the bind will never be used again. Closes the UDP socket and refuses
// future Open calls.
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

type outgoingPacket struct {
	to   *simEndpoint
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

// signal returns the channel that fires when recvQ has data. Kept for
// symmetry; inject() uses signalCh directly.
func (b *simBind) signal() <-chan struct{} {
	return b.signalCh
}

// ----------------------------------------------------------------------------
// Errors
// ----------------------------------------------------------------------------

type bindError string

func (e bindError) Error() string { return string(e) }

var (
	netErrClosed         = bindError("bind closed")
	netErrInvalidEndpoint = bindError("invalid endpoint type")
	netErrUnknownPeer    = bindError("unknown peer")
)