// SPDX-License-Identifier: MIT
//
// End-to-end test for the WireGuard mesh transport.
//
// Spins up two wg peers in the same process, both connected through a
// sim.Mesh bridge. PeerA dials peerB's mesh-private IP; the bytes are
// encrypted by wg, pushed through sim.Mesh (DDILBenign), and delivered
// to peerB's wg device, which decrypts and hands them to peerB's
// netstack. PeerB receives them as plaintext UDP.

package wg_test

import (
	"bytes"
	"crypto/rand"
	"net/netip"
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
	pub = pk
	return priv, pub
}

// TestWG_TwoPeerPing pings from peerA to peerB over the wg mesh.
func TestWG_TwoPeerPing(t *testing.T) {
	// Deterministic sim world.
	w, err := sim.NewWorld(sim.Config{
		Seed:        42,
		HostCount:   2,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	// Two peers with proper mesh IPs.
	privA, pubA := genWGKey(t)
	privB, pubB := genWGKey(t)

	peerA := &wg.Peer{
		ID:                  wg.PeerID("h0"),
		MeshAddr:            netip.MustParseAddr("10.42.0.1"),
		PublicKey:           pubA,
		PrivateKey:          privA,
		PersistentKeepalive: 1,
	}
	peerB := &wg.Peer{
		ID:                  wg.PeerID("h1"),
		MeshAddr:            netip.MustParseAddr("10.42.0.2"),
		PublicKey:           pubB,
		PrivateKey:          privB,
		PersistentKeepalive: 1,
	}

	mesh := wg.NewMesh(1420, nil)
	if err := mesh.AddPeer(peerA); err != nil {
		t.Fatal(err)
	}
	if err := mesh.AddPeer(peerB); err != nil {
		t.Fatal(err)
	}

	// StartAll: pass 1 alloc binds, pass 2 build devices.
	networks, err := mesh.StartAll()
	if err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	defer mesh.Close()

	netA := networks["h0"]
	netB := networks["h1"]
	if netA == nil || netB == nil {
		t.Fatalf("missing nets: A=%v B=%v", netA, netB)
	}

	// Start bridge between the wg peers and sim.Mesh.
	stopBridge, err := mesh.RunBridge(w)
	if err != nil {
		t.Fatalf("RunBridge: %v", err)
	}
	defer stopBridge()

	// PeerB listens on its mesh IP, port 7777.
	recvAddr := netip.AddrPortFrom(peerB.MeshAddr, 7777)
	bconn, err := netB.ListenUDPAddrPort(recvAddr)
	if err != nil {
		t.Fatalf("peerB listen: %v", err)
	}
	defer bconn.Close()

	gotCh := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 1500)
		n, _, err := bconn.ReadFrom(buf)
		if err != nil {
			t.Logf("peerB read: %v (net=%T)", err, bconn)
			return
		}
		gotCh <- buf[:n]
	}()

	// PeerA dials peerB's mesh IP:7777.
	msg := []byte("hello, federation")
	dialAddr := netip.AddrPortFrom(peerB.MeshAddr, 7777)
	dconn, err := netA.DialUDPAddrPort(netip.AddrPort{}, dialAddr)
	if err != nil {
		t.Fatalf("peerA dial: %v", err)
	}
	defer dconn.Close()

	// Drive the sim while handshakes + datagrams traverse the bridge.
// First wait long enough for wg handshakes to complete.
for i := 0; i < 40; i++ {
	w.Advance(50 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
}
t.Logf("handshake settle complete, sending payload")
	// We tick the world and give the bridge time to deliver.
	if _, err := dconn.Write(msg); err != nil {
		t.Fatalf("peerA write: %v", err)
	}
	t.Logf("peerA wrote %d bytes, polling for reply", len(msg))

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		w.Advance(50 * time.Millisecond)
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
	t.Fatal("peerB did not receive the message in 10s")
}