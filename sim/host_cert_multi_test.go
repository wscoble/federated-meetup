// SPDX-License-Identifier: AGPL-3.0
//
// Multiple host certs scenario: a single hostname can have multiple
// valid certs at different times (key rotation). The unique cert
// identifier is (hostname, host_tls_key, not_after), so two certs
// for the same hostname with different TLS keys land at distinct
// storage keys and coexist.
//
// What this exercises:
//   - Two ISSUE_HOST_CERT for the same hostname but different TLS
//     keys both succeed and produce distinct KV entries
//   - A REVOKE_HOST_CERT for ONE of them tombstones only that one
//     (the other remains valid)
//   - Cross-host convergence on all transitions
//
// Why this matters: TLS key rotation is a routine operation. The
// protocol must support overlapping certs (new key issued before
// old key expires) so that handoffs don't cause client-visible
// outages. A naïve design that keyed certs by hostname alone would
// break rotation.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestHostCert_MultipleCertsSameHostname walks through:
//  1. Vegas Programmers exist
//  2. ISSUE_HOST_CERT for "example.com" with TLS key A, validity 30d
//  3. ISSUE_HOST_CERT for "example.com" with TLS key B, validity 60d
//     (rotation: new key issued before old key expires)
//  4. Both cert entries coexist at distinct storage keys
//  5. REVOKE_HOST_CERT for (example.com, key A, 30d)
//  6. Cert A is tombstoned, cert B remains valid
//  7. All 4 hosts converge
func TestHostCert_MultipleCertsSameHostname(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        78,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	mesh := sim.NewMesh(w, sim.DDILBenign)
	w.AttachMesh(mesh)

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-cert: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}

	tlsKeyA := keyPairFromSeed(w, "tls-key-A")
	tlsKeyB := keyPairFromSeed(w, "tls-key-B")
	hostname := "rotation-test.example.com"
	notAfterA := w.Now().Add(30 * 24 * time.Hour)
	notAfterB := w.Now().Add(60 * 24 * time.Hour)
	notBefore := w.Now()

	certA := &pb.IssueHostCertPayload{
		Hostname:   hostname,
		HostTlsKey: &pb.PublicKey{Raw: tlsKeyA.Public[:]},
		NotBefore:  timestamppb.New(notBefore),
		NotAfter:   timestamppb.New(notAfterA),
		HostChallengeSignature: &pb.Signature{Raw: make([]byte, 64)},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ISSUE_HOST_CERT keyA",
		pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		certA,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootA := w.Hosts()[0].State(gkp.Public).Root()
	if rootA == parentRoot {
		t.Fatal("ISSUE_HOST_CERT A did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != rootA {
			t.Fatalf("post-cert-A divergence: host %s=%x want %x", h.ID(), got, rootA)
		}
	}
	t.Logf("cert A issued; root = %x", rootA[:4])

	certB := &pb.IssueHostCertPayload{
		Hostname:   hostname,
		HostTlsKey: &pb.PublicKey{Raw: tlsKeyB.Public[:]},
		NotBefore:  timestamppb.New(notBefore),
		NotAfter:   timestamppb.New(notAfterB),
		HostChallengeSignature: &pb.Signature{Raw: make([]byte, 64)},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ISSUE_HOST_CERT keyB",
		pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		certB,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootB := w.Hosts()[0].State(gkp.Public).Root()
	if rootB == rootA {
		t.Fatal("ISSUE_HOST_CERT B did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != rootB {
			t.Fatalf("post-cert-B divergence: host %s=%x want %x", h.ID(), got, rootB)
		}
	}
	t.Logf("cert B issued; root = %x", rootB[:4])

	// Verify both cert entries coexist with distinct keys.
	keyA := fmtCertKey(hostname, tlsKeyA.Public, notAfterA)
	keyB := fmtCertKey(hostname, tlsKeyB.Public, notAfterB)
	if keyA == keyB {
		t.Fatalf("cert storage keys should differ: %s == %s", keyA, keyB)
	}
	verifyCertEntryPresent(t, w.Hosts()[0].State(gkp.Public), keyA)
	verifyCertEntryPresent(t, w.Hosts()[0].State(gkp.Public), keyB)

	// Revoke A only.
	revokeA := &pb.RevokeHostCertPayload{
		Hostname:   hostname,
		HostTlsKey: &pb.PublicKey{Raw: tlsKeyA.Public[:]},
		NotAfter:   timestamppb.New(notAfterA),
		Reason:     "key rotation",
	}
	if !applyBroadcastFor(t, w, gkp.Public, "REVOKE_HOST_CERT keyA",
		pb.TransitionType_TRANSITION_TYPE_REVOKE_HOST_CERT,
		revokeA,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	rootAfterRevoke := w.Hosts()[0].State(gkp.Public).Root()
	if rootAfterRevoke == rootB {
		t.Fatal("REVOKE did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != rootAfterRevoke {
			t.Fatalf("post-revoke divergence: host %s=%x want %x", h.ID(), got, rootAfterRevoke)
		}
	}

	// Cert A is tombstoned, cert B remains valid.
	verifyCertEntryTombstoned(t, w.Hosts()[0].State(gkp.Public), keyA)
	verifyCertEntryPresent(t, w.Hosts()[0].State(gkp.Public), keyB)
	t.Logf("cert A tombstoned, cert B still valid; root = %x", rootAfterRevoke[:4])
}

// verifyCertEntryPresent and verifyCertEntryTombstoned are
// defined in host_cert_test.go and reused here.