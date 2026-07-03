// SPDX-License-Identifier: AGPL-3.0
//
// Composite scenario: a realistic Vegas Programmers lifecycle that
// chains together transitions we covered individually in earlier
// cycles, into a single coherent story. This is the Deming
// "Act" phase — turning individual cycle learnings into a
// reproducible composite that exercises the full state machine
// in production-realistic order.
//
// What this exercises:
//   - CREATE_GROUP -> ADD_STEWARD -> DECLARE_STEWARD_CUSTODY ->
//     CREATE_EVENT -> RSVP -> ISSUE_HOST_CERT -> NAME_BIND ->
//     SLASH_STEWARD -> ATTEST -> CANCEL_RSVP
//   - 10 transitions across 4 hosts, fully convergent
//   - The transition types covered individually in cycles 8-13,
//     now interacting as one realistic group lifecycle
//
// Why this matters: tests that pass in isolation can fail when
// composed (state ordering, equivocation log interference,
// KV-entry collisions). Composite scenarios catch integration
// bugs the per-transition tests can't.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestComposite_RealisticGroupLifecycle walks through:
//  1. CREATE_GROUP: alice + bob, threshold 2
//  2. ADD_STEWARD: carol joins (now 3 stewards, threshold 2)
//  3. Alice DECLARE_STEWARD_CUSTODY: HSM
//  4. Bob DECLARE_STEWARD_CUSTODY: HARDWARE_WALLET
//  5. CREATE_EVENT: meetup-1
//  6. RSVP: eve RSVPs to meetup-1
//  7. ISSUE_HOST_CERT: vegas-programmers.example.com
//  8. NAME_BIND: "vegas-programmers" globally
//  9. Carol equivocated (fabricated evidence); alice + bob SLASH her
// 10. ATTEST: alice endorses eve as an organizer
// 11. CANCEL_RSVP: eve cancels her RSVP
//
// At each step, the state root advances and all 4 hosts converge.
func TestComposite_RealisticGroupLifecycle(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        60,
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
	stewards := stewardKPsForTest(w) // [alice, bob, carol]
	carol := stewards[2]
	dave := keyPairFromSeed(w, "dave") // we'll use carol, not dave, since carol is in setup
	eve := keyPairFromSeed(w, "eve-organizer")

	// Step 2: ADD_STEWARD dave. Wait — setupVegasProgrammers
	// already includes dave? Let me check. No — stewards from
	// setupVegasProgrammers are [alice, bob, carol]. We add dave.
	davePayload := &pb.AddStewardPayload{
		NewSteward: &pb.PublicKey{Raw: dave.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ADD_STEWARD dave",
		pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD,
		davePayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root1 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root1, "after ADD_STEWARD dave")

	// Step 3: alice HSM custody.
	aliceCustody := &pb.DeclareStewardCustodyPayload{
		Steward: &pb.PublicKey{Raw: stewards[0].Public[:]},
		Tier:    pb.CustodyTier_CUSTODY_TIER_HSM,
	}
	if !applyBroadcastFor(t, w, gkp.Public, "alice HSM",
		pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY,
		aliceCustody,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root2 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root2, "after alice HSM custody")

	// Step 4: bob HARDWARE_WALLET.
	bobCustody := &pb.DeclareStewardCustodyPayload{
		Steward: &pb.PublicKey{Raw: stewards[1].Public[:]},
		Tier:    pb.CustodyTier_CUSTODY_TIER_HARDWARE_WALLET,
	}
	if !applyBroadcastFor(t, w, gkp.Public, "bob HARDWARE_WALLET",
		pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY,
		bobCustody,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root3 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root3, "after bob HARDWARE_WALLET custody")

	// Step 5: CREATE_EVENT meetup-1.
	eventID := "meetup-1"
	eventPayload := &pb.CreateEventPayload{
		EventId:  eventID,
		Title:    "Vegas Programmers — Inaugural Meetup",
		StartsAt: timestamppb.New(w.Now().Add(7 * 24 * time.Hour)),
		EndsAt:   timestamppb.New(w.Now().Add(7*24*time.Hour + 2*time.Hour)),
		Capacity: 50,
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CREATE_EVENT "+eventID,
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		eventPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root4 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root4, "after CREATE_EVENT meetup-1")

	// Step 6: RSVP eve.
	rsvpPayload := &pb.RsvpPayload{
		EventId: eventID,
		User:    &pb.PublicKey{Raw: eve.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "RSVP eve",
		pb.TransitionType_TRANSITION_TYPE_RSVP,
		rsvpPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root5 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root5, "after RSVP eve")

	// Step 7: ISSUE_HOST_CERT.
	hostTLSKP := keyPairFromSeed(w, "host-tls-key")
	hostname := "vegas-programmers.example.com"
	notAfter := w.Now().Add(365 * 24 * time.Hour)
	certPayload := &pb.IssueHostCertPayload{
		Hostname:   hostname,
		HostTlsKey: &pb.PublicKey{Raw: hostTLSKP.Public[:]},
		NotBefore:  timestamppb.New(w.Now()),
		NotAfter:   timestamppb.New(notAfter),
		HostChallengeSignature: &pb.Signature{
			Raw: make([]byte, 64), // state machine doesn't re-verify
		},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ISSUE_HOST_CERT "+hostname,
		pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT,
		certPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root6 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root6, "after ISSUE_HOST_CERT")

	// Step 8: NAME_BIND globally.
	bindPayload := &pb.NameBindPayload{
		Name:     "vegas-programmers",
		NotAfter: timestamppb.New(notAfter),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND vegas-programmers",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND,
		bindPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root7 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root7, "after NAME_BIND")

	// Step 9: SLASH_STEWARD carol (equivocation evidence).
	prior := root7
	txHashA, txHashB := fabricateEquivocationHashes(t, gkp.Public, prior)
	slashProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_SLASH_STEWARD,
		PriorState: &pb.StateRoot{Hash: prior[:]},
		Payload: &pb.Transition_SlashSteward{SlashSteward: &pb.SlashStewardPayload{
			SlashedSteward: &pb.PublicKey{Raw: carol.Public[:]},
			PriorState:     &pb.StateRoot{Hash: prior[:]},
			HlcA:           []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0},
			HlcB:           []byte{0, 0, 0, 0, 0, 0, 0, 2, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0},
			TxHashA:        txHashA[:],
			TxHashB:        txHashB[:],
		}},
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(slashProto)
	if err != nil {
		t.Fatal(err)
	}
	slashSigs := make([]*pb.Signature, 0, 2)
	for _, k := range []crypto.KeyPair{stewards[0], stewards[1]} { // alice + bob, NOT carol
		s := crypto.Sign(k, gkp.Public, crypto.MsgKindTransition, canonical)
		slashSigs = append(slashSigs, &pb.Signature{Raw: s[:]})
	}
	slashProto.StewardSignatures = &pb.Multisig{Threshold: 2, Signatures: slashSigs}
	slashTx, err := group.NewTransition(slashProto, gkp.Public)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gkp.Public, slashTx); err != nil {
			t.Fatalf("SLASH on host %s: %v", h.ID(), err)
		}
	}
	w.Advance(50 * time.Millisecond)
	root8 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root8, "after SLASH_STEWARD carol")

	// Verify carol is gone, alice + bob + dave remain (3 stewards).
	stAfter := w.Hosts()[0].State(gkp.Public).StewardsAt(nil)
	if len(stAfter) != 3 {
		t.Fatalf("expected 3 stewards post-slash, got %d", len(stAfter))
	}
	for _, st := range stAfter {
		if st.Key == carol.Public {
			t.Errorf("carol should be slashed, but is still a steward")
		}
	}

	// Step 10: ATTEST alice -> eve, schema "endorsement.organizer".
	// Note: prior_state is root8 (post-slash), and we sign with
	// alice + bob (both still stewards). The threshold is still 2.
	attestPayload := &pb.AttestPayload{
		FromIdentity: &pb.PublicKey{Raw: stewards[0].Public[:]},
		ToIdentity:   &pb.PublicKey{Raw: eve.Public[:]},
		Schema:       "endorsement.organizer",
		Payload:      []byte("eve is a great organizer"),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "ATTEST alice->eve",
		pb.TransitionType_TRANSITION_TYPE_ATTEST,
		attestPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root9 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root9, "after ATTEST alice->eve")

	// Step 11: CANCEL_RSVP eve.
	cancelPayload := &pb.CancelRsvpPayload{
		EventId: eventID,
		User:    &pb.PublicKey{Raw: eve.Public[:]},
	}
	if !applyBroadcastFor(t, w, gkp.Public, "CANCEL_RSVP eve",
		pb.TransitionType_TRANSITION_TYPE_CANCEL_RSVP,
		cancelPayload,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	root10 := w.Hosts()[0].State(gkp.Public).Root()
	convergeCheck(t, w, gkp.Public, root10, "after CANCEL_RSVP eve")

	// Final state assertion: all the recorded entries should be present
	// in the snapshot.
	final := w.Hosts()[0].State(gkp.Public)
	keys := map[string]bool{
		"event/" + eventID:           true,
		"slashed/" + tlsKeyHex(carol.Public): true,
		"attest/" + tlsKeyHex(eve.Public):    true,
		"name_bind/*/vegas-programmers/" + itoa(notAfter.Unix()): true,
	}
	for k := range keys {
		found := false
		for _, e := range final.Snapshot().Entries {
			if e.Key == k && e.Value != nil {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected entry %q in final snapshot, not found", k)
		}
	}
	t.Logf("composite complete: 10 transitions, 4 hosts converged at root %x", root10[:4])
}

// convergeCheck asserts that all 4 hosts agree on the current state
// root for the given group. Used at each step of the composite
// scenario to fail fast when integration issues arise.
func convergeCheck(t *testing.T, w *sim.World, gid types.GroupID, expected types.Hash, label string) {
	t.Helper()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gid).Root(); got != expected {
			t.Fatalf("%s: host %s diverged: %x want %x", label, h.ID(), got, expected)
		}
	}
}

// fabricateEquivocationHashes produces two distinct tx hashes that
// could plausibly correspond to two equivocating transitions at the
// given prior_state. We don't apply these transitions — they're just
// the evidence payload for SLASH_STEWARD.
func fabricateEquivocationHashes(t *testing.T, gid types.GroupID, prior types.Hash) (types.Hash, types.Hash) {
	t.Helper()
	priorPB := &pb.StateRoot{Hash: prior[:]}
	mk := func(id string) []byte {
		tr := &pb.Transition{
			Type:       pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
			PriorState: priorPB,
			Payload: &pb.Transition_CreateEvent{CreateEvent: &pb.CreateEventPayload{
				EventId: id,
				Title:   "equivocation evidence " + id,
			}},
		}
		c, err := group.MarshalCanonicalForSigningHelper(tr)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	rawA := sha256Sum(mk("equiv-a"))
	rawB := sha256Sum(mk("equiv-b"))
	var hA, hB types.Hash
	copy(hA[:], rawA[:])
	copy(hB[:], rawB[:])
	_ = gid
	_ = strings.TrimSpace // keep strings import alive
	return hA, hB
}