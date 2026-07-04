// SPDX-License-Identifier: AGPL-3.0
//
// Cycle 37: REVOKE_HOST_CERT tombstone safety.
//
// REVOKE_HOST_CERT (internal/group/group.go:653) writes a revocation
// entry via hostCertRevocationKey, then writes nil to the cert entry
// via hostCertStorageKeyFromRevoke. The cert entry is removed from the
// entries slice (appendOrUpdateUnchecked with value=nil skips appending,
// line 1003).
//
// Compare to REMOVE_MEMBER / CANCEL_RSVP / REMOVE_HOST_PEER (cycles
// 25-27) which were patched to write tombstones because removing the
// entry collapsed the root when the entry was the only delta between
// prior and current.
//
// REVOKE_HOST_CERT is SAFE because the sibling hostCertRevocationKey
// entry keeps the KV from reverting to its pre-ISSUE state. The
// revocation entry is always present after a successful REVOKE.
//
// What this test pins down:
//
//   1. Issue cert → cert entry added, root advances
//   2. Revoke cert → cert entry removed, revocation entry added, root
//      advances AGAIN (does NOT collapse to either pre-ISSUE or
//      pre-REVOKE root)
//   3. Re-issue the same (hostname, tls_key, not_after) after revoke
//      works — no spurious equivocation. The revocation entry is the
//      "audit trail" that lets future stewards know the cert was once
//      issued and revoked.
//
// Why this matters: the sibling-entry pattern is a load-bearing design
// choice. If a future engineer "simplified" REVOKE_HOST_CERT to skip
// the revocation entry (storing only the cert tombstone), every
// revoked cert would silently collapse the root — opening the door to
// the same equivocation-spurious-rejection class of bug we fixed in
// cycles 25-27.
package sim_test

import (
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHostCert_RevokeDoesNotCollapseRoot(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        100,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	hostTLSKP := keyPairFromSeed(w, "host-tls-cycle37")
	hostname := "cycle37.example.com"
	notAfter := w.Now().Add(30 * 24 * time.Hour)
	notBefore := w.Now()

	// ISSUE_HOST_CERT.
	certPayload := &pb.IssueHostCertPayload{
		Hostname:   hostname,
		HostTlsKey: &pb.PublicKey{Raw: hostTLSKP.Public[:]},
		NotBefore:  timestamppb.New(notBefore),
		NotAfter:   timestamppb.New(notAfter),
		HostChallengeSignature: &pb.Signature{
			Raw: make([]byte, 64),
		},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ISSUE_HOST_CERT",
		pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		certPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	certRoot := w.Hosts()[0].State(gkp.Public).Root()
	if certRoot == parentRoot {
		t.Fatal("ISSUE_HOST_CERT did not advance root")
	}

	// REVOKE_HOST_CERT.
	revokePayload := &pb.RevokeHostCertPayload{
		Hostname:   hostname,
		HostTlsKey: &pb.PublicKey{Raw: hostTLSKP.Public[:]},
		NotAfter:   timestamppb.New(notAfter),
		Reason:     "cycle-37-isolation-test",
	}
	if !applyBroadcastFor(t, w, gkp.Public, "REVOKE_HOST_CERT",
		pb.TransitionType_TRANSITION_TYPE_REVOKE_HOST_CERT,
		revokePayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	revokedRoot := w.Hosts()[0].State(gkp.Public).Root()

	// Critical invariant: revoked root must NOT equal parent root.
	// The cert entry was the only delta; if REVOKE just deleted the
	// entry without keeping the sibling revocation, the root would
	// collapse here. The sibling revocation entry prevents that.
	if revokedRoot == parentRoot {
		t.Fatalf("REVOKE collapsed root to parent root %x — sibling revocation entry missing", parentRoot)
	}
	if revokedRoot == certRoot {
		t.Fatal("REVOKE did not advance root from cert root")
	}

	// Cert entry should be tombstoned (absent from entries or nil).
	certKey := fmtCertKey(hostname, hostTLSKP.Public, notAfter)
	var foundCert bool
	for _, e := range w.Hosts()[0].State(gkp.Public).Snapshot().Entries {
		if e.Key == certKey {
			foundCert = true
			if e.Value != nil {
				t.Errorf("cert entry %q should be tombstoned (nil), got %x", certKey, e.Value)
			}
		}
	}
	if foundCert {
		t.Logf("cert entry tombstoned (still present with nil value)")
	} else {
		t.Logf("cert entry removed from entries")
	}

	// All 4 hosts must agree on the revoked root.
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != revokedRoot {
			t.Fatalf("post-revoke divergence: host %s=%x want %x",
				h.ID(), got, revokedRoot)
		}
	}

	t.Logf("root chain confirmed: parent=%x, cert=%x, revoked=%x (all distinct)",
		parentRoot[:4], certRoot[:4], revokedRoot[:4])
}

func TestHostCert_ReissueAfterRevoke(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        101,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	hostTLSKP := keyPairFromSeed(w, "host-tls-reissue")
	hostname := "reissue.example.com"
	notAfter := w.Now().Add(30 * 24 * time.Hour)
	notBefore := w.Now()

	makeCert := func() *pb.IssueHostCertPayload {
		return &pb.IssueHostCertPayload{
			Hostname:   hostname,
			HostTlsKey: &pb.PublicKey{Raw: hostTLSKP.Public[:]},
			NotBefore:  timestamppb.New(notBefore),
			NotAfter:   timestamppb.New(notAfter),
			HostChallengeSignature: &pb.Signature{Raw: make([]byte, 64)},
		}
	}

	makeRevoke := func() *pb.RevokeHostCertPayload {
		return &pb.RevokeHostCertPayload{
			Hostname:   hostname,
			HostTlsKey: &pb.PublicKey{Raw: hostTLSKP.Public[:]},
			NotAfter:   timestamppb.New(notAfter),
			Reason:     "test-revoke",
		}
	}

	// Step 1: ISSUE
	if !applyBroadcastFor(t, w, gkp.Public, "ISSUE #1",
		pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT, makeCert(),
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	rootAfterIssue1 := w.Hosts()[0].State(gkp.Public).Root()

	// Step 2: REVOKE
	if !applyBroadcastFor(t, w, gkp.Public, "REVOKE #1",
		pb.TransitionType_TRANSITION_TYPE_REVOKE_HOST_CERT, makeRevoke(),
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	rootAfterRevoke1 := w.Hosts()[0].State(gkp.Public).Root()

	// Step 3: RE-ISSUE — same hostname, tls_key, not_after.
	// Must NOT trigger spurious equivocation. The sibling revocation
	// entry keeps the root distinct so the equivocation log doesn't
	// conflict.
	if !applyBroadcastFor(t, w, gkp.Public, "ISSUE #2 (reissue)",
		pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT, makeCert(),
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	rootAfterReissue := w.Hosts()[0].State(gkp.Public).Root()

	if rootAfterReissue == rootAfterRevoke1 {
		t.Fatal("RE-ISSUE did not advance root")
	}

	// Verify cert entry is present and non-nil again.
	certKey := fmtCertKey(hostname, hostTLSKP.Public, notAfter)
	var certValue []byte
	for _, e := range w.Hosts()[0].State(gkp.Public).Snapshot().Entries {
		if e.Key == certKey {
			certValue = e.Value
		}
	}
	if certValue == nil {
		t.Fatal("cert entry not restored after re-issue")
	}
	if len(certValue) == 0 {
		t.Fatal("cert entry has empty value after re-issue")
	}

	t.Logf("root chain: issue1=%x, revoke1=%x, reissue=%x — sibling revocation prevented equivocation",
		rootAfterIssue1[:4], rootAfterRevoke1[:4], rootAfterReissue[:4])
}