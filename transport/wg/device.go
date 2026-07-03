// SPDX-License-Identifier: AGPL-3.0
//
// Package wg: device bring-up and UAPI configuration.
//
// Architecture (v3 — sim.Mesh-bridged kernel UDP):
//
//   StartPeer:
//     1. Creates a netstack TUN bound to peer's MeshAddr.
//     2. Creates a simBind (real localhost UDP socket, sim.Mesh-bridged).
//     3. Creates a wireguard-go Device over (tun, simBind).
//     4. Applies UAPI config: this peer's private_key + every other
//        peer's public_key/endpoint/allowed_ip.
//     5. Returns the *netstack.Net so the caller can Dial/Listen.
//
//   Bootstrap ordering matters: every peer's simBind (with its kernel UDP
//   port) must be allocated BEFORE any peer's IpcSet is called, because
//   each peer's UAPI config references every other peer's endpoint.
//   `Mesh.StartAll` performs the two passes atomically.

package wg

import (
	"fmt"
	"net/netip"

	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Net is the alias for the netstack's *Net.
type Net = netstack.Net

// StartAll brings up WireGuard devices for every peer in the mesh in two
// passes: (1) allocate every peer's simBind + UDP port; (2) for each peer,
// build the wg device and apply UAPI. Returns the netstack.Net for each
// peer keyed by PeerID.
//
// This is the correct bootstrap for a federation: every endpoint must be
// reachable before any peer's IpcSet runs, because each peer's config
// references every other peer's endpoint.
func (m *Mesh) StartAll() (map[PeerID]*Net, error) {
	m.mu.Lock()
	peers := make([]*Peer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	m.mu.Unlock()

	// Pass 1: allocate simBind + kernel UDP port for every peer.
	for _, p := range peers {
		if _, ok := m.peerBinds[p.ID]; ok {
			continue
		}
		bind, err := newSimBind(p.ID, p.MeshAddr)
		if err != nil {
			return nil, fmt.Errorf("wg: alloc bind for %s: %w", p.ID, err)
		}
		bind.initPlumbing(m)
		m.mu.Lock()
		m.peerBinds[p.ID] = bind
		m.byKernelAddr[bind.LocalAddr().String()] = p.ID
		m.mu.Unlock()
	}

	// Pass 2: for each peer, build the device and apply UAPI.
	out := make(map[PeerID]*Net, len(peers))
	for _, p := range peers {
		net, err := m.startPeerLocked(p)
		if err != nil {
			return nil, err
		}
		out[p.ID] = net
	}
	return out, nil
}

// startPeerLocked brings up a single peer. Caller must have already
// allocated all peers' simBinds.
func (m *Mesh) startPeerLocked(p *Peer) (*Net, error) {
	m.mu.Lock()
	bind, ok := m.peerBinds[p.ID]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("wg: peer %s has no bind; call StartAll", p.ID)
	}

	tun, net, err := netstack.CreateNetTUN(
		[]netip.Addr{p.MeshAddr},
		nil,
		m.mtu,
	)
	if err != nil {
		return nil, fmt.Errorf("wg: create net TUN for %s: %w", p.ID, err)
	}

	logger := device.NewLogger(device.LogLevelError, "wg/"+string(p.ID)+": ")
	dev := device.NewDevice(tun, bind, logger)

	cfg := buildUAPIConfig(p, m)
	if err := dev.IpcSet(cfg); err != nil {
		bind.Close()
		dev.Close()
		return nil, fmt.Errorf("wg: ipc set for %s: %w", p.ID, err)
	}

	// Bring the device up so it actually starts crypto routines.
	if err := dev.Up(); err != nil {
		bind.Close()
		dev.Close()
		return nil, fmt.Errorf("wg: up %s: %w", p.ID, err)
	}

	m.mu.Lock()
	p.Device = &WireGuardDevice{
		peerID:   p.ID,
		meshAddr: p.MeshAddr,
		private:  p.PrivateKey,
		Device:   dev,
		bind:     bind,
		closed:   make(chan struct{}),
	}
	m.mu.Unlock()

	return net, nil
}

// StartPeer brings up a single peer. DEPRECATED for federation bootstrap —
// prefer StartAll. Kept for tests that only need one peer.
func (m *Mesh) StartPeer(p *Peer) (*Net, error) {
	m.mu.Lock()
	if _, ok := m.peers[p.ID]; !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("wg: peer %s not in mesh", p.ID)
	}
	m.mu.Unlock()

	if _, ok := m.peerBinds[p.ID]; !ok {
		bind, err := newSimBind(p.ID, p.MeshAddr)
		if err != nil {
			return nil, fmt.Errorf("wg: create sim bind for %s: %w", p.ID, err)
		}
		bind.initPlumbing(m)
		m.mu.Lock()
		m.peerBinds[p.ID] = bind
		m.mu.Unlock()
	}

	return m.startPeerLocked(p)
}

// buildUAPIConfig constructs the UAPI config string for a peer.
//
// Format:
//	private_key=<hex>
//	listen_port=<port>      # 0 = kernel-assigned ephemeral
//	public_key=<hex>        # one per remote peer
//	endpoint=<host:port>    # the remote peer's loopback UDP endpoint
//	allowed_ip=<cidr>       # the remote peer's mesh IP /32
//	persistent_keepalive_interval=<seconds>
func buildUAPIConfig(p *Peer, m *Mesh) string {
	cfg := fmt.Sprintf("private_key=%x\nlisten_port=%d\n", p.PrivateKey, 0)
	for _, remote := range m.peers {
		if remote.ID == p.ID {
			continue
		}
		cfg += fmt.Sprintf("public_key=%x\n", remote.PublicKey)
		m.mu.Lock()
		var endpoint string
		if rb, ok := m.peerBinds[remote.ID]; ok && rb != nil {
			endpoint = rb.LocalAddr().String()
		} else {
			endpoint = "127.0.0.1:0"
		}
		m.mu.Unlock()
		cfg += fmt.Sprintf("endpoint=%s\n", endpoint)
		cfg += fmt.Sprintf("allowed_ip=%s/32\n", remote.MeshAddr.String())
		if p.PersistentKeepalive > 0 {
			cfg += fmt.Sprintf("persistent_keepalive_interval=%d\n", p.PersistentKeepalive)
		}
	}
	return cfg
}