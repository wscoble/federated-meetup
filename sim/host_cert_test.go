// SPDX-License-Identifier: AGPL-3.0
//
// Host certificate scenario: a steward group issues a TLS cert
// for a host to serve under a specific hostname, then revokes
// it.
//
// What this exercises:
//   - ISSUE_HOST_CERT: records host_cert/{hostname}/{tls_key}/{not_after}
//     in the Merkle KV
//   - REVOKE_HOST_CERT: writes a revocation entry AND tombstones
//     the cert entry
//   - Cross-host convergence on both transitions
//
// Why this matters: TLS cert issuance is the gate that turns
// federation into a usable public surface. Without it, clients
// can't verify they're talking to the real host for a group.
// Without revocation, a compromised cert stays valid until expiry.
package sim_test

import (
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	"github.com/wscoble/federated-meetup/internal/types"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"github.com/wscoble/federated-meetup/sim"
)

// TestHostCert_IssueThenRevoke walks through:
//  1. Vegas Programmers exist
//  2. Stewards sign ISSUE_HOST_CERT for "vegas-programmers.example.com"
//     with a host TLS key and a 30-day validity window
//  3. Cert entry appears in the snapshot on all 4 hosts
//  4. Stewards sign REVOKE_HOST_CERT for the same (hostname, key, not_after)
//  5. Cert entry is tombstoned (value=nil) on all hosts
func TestHostCert_IssueThenRevoke(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        48,
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

	// Pre-state: parent group converged.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-cert: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}

	// Generate a "host TLS key" for the cert. (Real life would use
	// a TLS X25519 key; here we just use Ed25519 as a stand-in.)
	hostTLSKP := keyPairFromSeed(w, "host-tls-key")
	hostname := "vegas-programmers.example.com"
	notAfter := w.Now().Add(30 * 24 * time.Hour)
	notBefore := w.Now()

	// Step 2: ISSUE_HOST_CERT.
	certPayload := &pb.IssueHostCertPayload{
		Hostname:    hostname,
		HostTlsKey:  &pb.PublicKey{Raw: hostTLSKP.Public[:]},
		NotBefore:   timestamppb.New(notBefore),
		NotAfter:    timestamppb.New(notAfter),
		HostChallengeSignature: &pb.Signature{
			Raw: func() []byte {
				// In real life the host signs the canonical cert bytes.
				// Here we use a placeholder; the state machine doesn't
				// re-verify the host signature at apply time.
				return make([]byte, 64)
			}(),
		},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ISSUE_HOST_CERT "+hostname,
		pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		certPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Step 3: cert entry should be present on all hosts.
	certKey := fmtCertKey(hostname, hostTLSKP.Public, notAfter)
	certRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != certRoot {
			t.Fatalf("post-cert divergence: host %s=%x want %x", h.ID(), got, certRoot)
		}
	}
	verifyCertEntryPresent(t, w.Hosts()[0].State(gkp.Public), certKey)
	t.Logf("cert issued; root = %x", certRoot[:4])

	// Step 4: REVOKE_HOST_CERT.
	revokePayload := &pb.RevokeHostCertPayload{
		Hostname:   hostname,
		HostTlsKey: &pb.PublicKey{Raw: hostTLSKP.Public[:]},
		NotAfter:   timestamppb.New(notAfter),
		Reason:     "rotation",
	}
	if !applyBroadcastFor(t, w, gkp.Public, "REVOKE_HOST_CERT "+hostname,
		pb.TransitionType_TRANSITION_TYPE_REVOKE_HOST_CERT,
		revokePayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Step 5: cert entry should be tombstoned (value=nil) on all hosts.
	revokedRoot := w.Hosts()[0].State(gkp.Public).Root()
	if revokedRoot == certRoot {
		t.Fatalf("REVOKE did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != revokedRoot {
			t.Fatalf("post-revoke divergence: host %s=%x want %x", h.ID(), got, revokedRoot)
		}
	}
	verifyCertEntryTombstoned(t, w.Hosts()[0].State(gkp.Public), certKey)
	t.Logf("cert revoked; root = %x", revokedRoot[:4])
}

// fmtCertKey formats a cert storage key the same way the state
// machine does (hostCertStorageKey in gates.go).
func fmtCertKey(hostname string, tlsKey types.PublicKey, notAfter time.Time) string {
	notAfterSeconds := notAfter.Unix()
	// %x on a [32]byte prints the full 64-hex string
	return "host_cert/" + hostname + "/" + tlsKeyHex(tlsKey) + "/" + itoa(notAfterSeconds)
}

// tlsKeyHex is a tiny helper — formatting a [32]byte as lowercase hex.
func tlsKeyHex(k types.PublicKey) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, 64)
	for i := 0; i < 32; i++ {
		out[i*2] = hexchars[k[i]>>4]
		out[i*2+1] = hexchars[k[i]&0xf]
	}
	return string(out)
}

// itoa converts an int64 to its decimal string form without pulling
// in strconv directly in this file (keeps the test imports minimal).
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// verifyCertEntryPresent asserts that the entry with the given key
// exists in the snapshot with a non-nil value.
func verifyCertEntryPresent(t *testing.T, st *group.State, key string) {
	t.Helper()
	for _, e := range st.Snapshot().Entries {
		if e.Key == key {
			if e.Value == nil {
				t.Errorf("entry %q present but value is nil", key)
			}
			return
		}
	}
	t.Errorf("entry %q not found in snapshot", key)
}

// verifyCertEntryTombstoned asserts that the entry with the given
// key is absent from the snapshot OR present with a nil value (the
// appendOrUpdate path with value=nil removes the entry entirely).
func verifyCertEntryTombstoned(t *testing.T, st *group.State, key string) {
	t.Helper()
	for _, e := range st.Snapshot().Entries {
		if e.Key == key {
			if e.Value != nil {
				t.Errorf("entry %q still present with non-nil value %x", key, e.Value)
			}
			return
		}
	}
	// Absence is also a valid tombstone — appendOrUpdate with value=nil
	// removes the entry entirely.
}