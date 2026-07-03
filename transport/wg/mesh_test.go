// SPDX-License-Identifier: AGPL-3.0
//
// End-to-end test for the WireGuard mesh transport.
//
// Two layers of verification:
//
//   1. TestWG_HandshakeCompletes — proves wg peers can establish a session
//      through the sim.Mesh-bridged loopback. Asserts that we see the
//      expected number of handshake packets flowing both directions within
//      a bounded time budget. No data payload, no netstack dependency.
//
//   2. TestWG_TwoPeerPing — full end-to-end: peerA writes a UDP datagram
//      to peerB's mesh-private IP, peerB receives it as plaintext UDP.
//      Exercises the netstack TUN, wg crypto, sim.Mesh, and the bridge.
//
// The handshake-only test is the smaller, more reliable unit. The
// data-payload test is the "this actually federates" proof.

package wg_test

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/sscoble/federated-meetup/sim"
	"github.com/sscoble/federated-meetup/transport/wg"
)

// genWGKey generates a Curve25519 keypair for WireGuard.
func genWGKey(t *testing.T) (priv, pub []byte) {
	t.Helper()
	priv = make([]byte, 32)
	if _, err := rand.Read(priv); err != nil {
		t.Fatal(err)
	}
	pk, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pk
}

// makeTestWorld creates a sim.World with N hosts and a benign DDIL mesh.
func makeTestWorld(t *testing.T, n int) *sim.World {
	t.Helper()
	w, err := sim.NewWorld(sim.Config{
		Seed:        42,
		HostCount:   n,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))
	return w
}

// TestWG_HandshakeCompletes verifies that two wg peers exchange the
// expected set of handshake messages through the sim.Mesh bridge.
//
// Why this is the smallest reliable unit: wg-go's handshake is what
// proves "this peer is who they say they are, end-to-end encrypted".
// Once the handshake completes, the data-payload test can run with a
// reasonable wall-clock budget. This test asserts that boundary.
func TestWG_HandshakeCompletes(t *testing.T) {
	w := makeTestWorld(t, 2)
	defer w.Close()

	privA, pubA := genWGKey(t)
	privB, pubB := genWGKey(t)

	mesh := wg.NewMesh(1420, nil)
	peerA := &wg.Peer{ID: "h0", MeshAddr: netip.MustParseAddr("10.42.0.1"),
		PublicKey: pubA, PrivateKey: privA, PersistentKeepalive: 1}
	peerB := &wg.Peer{ID: "h1", MeshAddr: netip.MustParseAddr("10.42.0.2"),
		PublicKey: pubB, PrivateKey: privB, PersistentKeepalive: 1}
	if err := mesh.AddPeer(peerA); err != nil {
		t.Fatal(err)
	}
	if err := mesh.AddPeer(peerB); err != nil {
		t.Fatal(err)
	}

	if _, err := mesh.StartAll(); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	defer mesh.Close()

	stopBridge, err := mesh.RunBridge(w)
	if err != nil {
		t.Fatal(err)
	}
	defer stopBridge()

	// Drive the sim for up to 8 wall-clock seconds (handshake should
	// complete in well under 1s normally). We advance virtual time +
	// drain + sleep to let wg-go timers fire.
	deadline := time.Now().Add(8 * time.Second)
	var inits, resps, dataPackets atomic.Int32
	for time.Now().Before(deadline) {
		w.Advance(50 * time.Millisecond)
		// Inspect packets WITHOUT consuming them — use Peek so the
		// bridge can still deliver them to the wg peers.
		for _, msg := range w.Mesh().Peek() {
			if len(msg.Payload) == 0 {
				continue
			}
			switch msg.Payload[0] {
			case 1:
				inits.Add(1)
			case 2:
				resps.Add(1)
			case 4:
				dataPackets.Add(1)
			}
		}
		mesh.Drain(w)

		// Handshake is "complete enough" for our purposes when we've
		// seen at least 2 Inits and 2 Resps (one round-trip + the
		// rekey keepalive).
		if inits.Load() >= 2 && resps.Load() >= 2 {
			t.Logf("handshake observed: inits=%d resps=%d data=%d",
				inits.Load(), resps.Load(), dataPackets.Load())
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("handshake did not complete in 8s: inits=%d resps=%d data=%d",
		inits.Load(), resps.Load(), dataPackets.Load())
}

// TestWG_TwoPeerPing is the full end-to-end federation proof.
// PeerA writes a UDP datagram to peerB's mesh-private IP; peerB
// receives it as plaintext UDP, decrypted by the wg tunnel and
// delivered via netstack.
//
// Strategy: wait for the handshake to complete (via IpcGet's
// last_handshake_time) before writing the payload. This separates the
// two timing concerns and makes the test deterministic.
func TestWG_TwoPeerPing(t *testing.T) {
	w := makeTestWorld(t, 2)
	defer w.Close()

	privA, pubA := genWGKey(t)
	privB, pubB := genWGKey(t)

	peerA := &wg.Peer{ID: "h0", MeshAddr: netip.MustParseAddr("10.42.0.1"),
		PublicKey: pubA, PrivateKey: privA, PersistentKeepalive: 1}
	peerB := &wg.Peer{ID: "h1", MeshAddr: netip.MustParseAddr("10.42.0.2"),
		PublicKey: pubB, PrivateKey: privB, PersistentKeepalive: 1}

	mesh := wg.NewMesh(1420, nil)
	if err := mesh.AddPeer(peerA); err != nil {
		t.Fatal(err)
	}
	if err := mesh.AddPeer(peerB); err != nil {
		t.Fatal(err)
	}

	networks, err := mesh.StartAll()
	if err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	defer mesh.Close()
	netA, netB := networks["h0"], networks["h1"]

	stopBridge, err := mesh.RunBridge(w)
	if err != nil {
		t.Fatal(err)
	}
	defer stopBridge()

	// peerB listens for the payload.
	bconn, err := netB.ListenUDPAddrPort(netip.AddrPortFrom(peerB.MeshAddr, 7777))
	if err != nil {
		t.Fatalf("peerB listen: %v", err)
	}
	defer bconn.Close()

	gotCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1500)
		n, _, err := bconn.ReadFrom(buf)
		if err != nil {
			t.Logf("peerB read: %v", err)
			return
		}
		gotCh <- buf[:n]
	}()

	// Wait for both peers to have completed a handshake before writing
	// the data payload. We poll IpcGet on each peer until we see a
	// non-zero last_handshake_time_sec.
	if err := waitForHandshake(peerA, peerB, mesh, w); err != nil {
		t.Fatal(err)
	}

	// peerA dials and writes.
	dconn, err := netA.DialUDPAddrPort(netip.AddrPort{}, netip.AddrPortFrom(peerB.MeshAddr, 7777))
	if err != nil {
		t.Fatalf("peerA dial: %v", err)
	}
	defer dconn.Close()

	msg := []byte("hello, federation")
	if _, err := dconn.Write(msg); err != nil {
		t.Fatalf("peerA write: %v", err)
	}

	// Wait up to 10s for the payload. Handshake is done, so this is
	// bounded by the data-path latency only.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		w.Advance(50 * time.Millisecond)
		mesh.Drain(w)
		select {
		case got := <-gotCh:
			if !bytes.Equal(got, msg) {
				t.Fatalf("peerB got %q, want %q", got, msg)
			}
			t.Logf("OK: wg transport delivered %d bytes end-to-end through sim.Mesh", len(got))
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("peerB did not receive the message in 10s after handshake")
}

// waitForHandshake drives the sim + bridge until both peers have
// completed at least one handshake. The wg device's IpcGet output
// includes last_handshake_time_sec — non-zero means a session exists.
func waitForHandshake(peerA, peerB *wg.Peer, mesh *wg.Mesh, w *sim.World) error {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		w.Advance(50 * time.Millisecond)
		mesh.Drain(w)
		if handshaken(peerA, mesh) && handshaken(peerB, mesh) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	// On timeout, dump IpcGet output for diagnostics.
	dump := func(p *wg.Peer) string {
		mesh.MeshLock().Lock()
		dev := p.Device
		mesh.MeshLock().Unlock()
		if dev == nil || dev.Device == nil {
			return "no device"
		}
		got, err := dev.Device.IpcGet()
		if err != nil {
			return err.Error()
		}
		return got
	}
	return fmt.Errorf("handshake did not complete within 8s\nA=%s\nB=%s",
		dump(peerA), dump(peerB))
}

// handshaken returns true if the peer has completed at least one
// handshake (i.e. last_handshake_time_sec > 0 in IpcGet output).
func handshaken(p *wg.Peer, mesh *wg.Mesh) bool {
	mesh.MeshLock().Lock()
	dev := p.Device
	mesh.MeshLock().Unlock()
	if dev == nil || dev.Device == nil {
		return false
	}
	got, err := dev.Device.IpcGet()
	if err != nil {
		return false
	}
	// IpcGet output is a flat key=value text. We look for any line
	// mentioning last_handshake_time_sec=N with N > 0.
	for _, line := range strings.Split(got, "\n") {
		if !strings.HasPrefix(line, "last_handshake_time_sec=") {
			continue
		}
		var sec int64
		_, err := fmt.Sscanf(line, "last_handshake_time_sec=%d", &sec)
		if err == nil && sec > 0 {
			return true
		}
	}
	return false
}