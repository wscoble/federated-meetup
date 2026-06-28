// SPDX-License-Identifier: MIT
//
// Fork scenario: a steward group forks into a new sovereign group.
//
// What this exercises:
//   - FORK transition on the parent group (records fork_lineage)
//   - New group state registered on all hosts via AddGroup
//   - Initial transition on the new group (CREATE_GROUP)
//   - Divergent evolution: the two groups evolve independently
//   - Cross-host convergence: both groups converge on all 4 hosts
//
// Why this matters: FORK is the load-bearing primitive for the
// federation model. When stewards disagree on direction, FORK lets
// the minority create a new sovereign group while preserving the
// shared history up to the fork point. This test proves the state
// machine supports that workflow.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// TestFork_NewSovereignGroup walks through:
//  1. Vegas Programmers exist (parent group, 3 stewards, threshold 2)
//  2. Stewards sign a FORK transition declaring a new group key + initial stewards
//  3. All hosts apply the FORK → parent's snapshot now has fork_lineage
//  4. Each host registers the new group via AddGroup
//  5. Stewards of the new group (a subset: alice + bob) sign CREATE_GROUP
//  6. The new group diverges: a CREATE_EVENT on the new group only
//  7. Both groups converge across all 4 hosts
func TestFork_NewSovereignGroup(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        43,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	mesh := sim.NewMesh(w, sim.DDILBenign)
	w.AttachMesh(mesh)

	gkp := setupVegasProgrammers(w) // already applies CREATE_GROUP on all 4 hosts
	stewards := stewardKPsForTest(w)

	// Sanity check: parent group has converged across all hosts.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent group not converged before fork: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}
	t.Logf("parent root pre-fork: %x", parentRoot)

	// Generate the new group's keypair. In real life, a forking
	// steward would generate this; here we just derive a deterministic
	// key from the world seed so the test is reproducible.
	newGKP := keyPairFromSeed(w, "forked-vegas-group")
	newGroupID := newGKP.Public
	t.Logf("new group ID: %x", newGroupID[:8])

	// The new group starts with alice and bob as initial stewards
	// (carol stays on the parent). Threshold 2.
	newInitialStewards := []crypto.KeyPair{stewards[0], stewards[1]} // alice + bob
	newStewardKeys := make([]*pb.PublicKey, len(newInitialStewards))
	for i, k := range newInitialStewards {
		newStewardKeys[i] = &pb.PublicKey{Raw: k.Public[:]}
	}

	// Step 1: Sign and broadcast the FORK transition on the parent.
	// applyBroadcast takes (group, kind, innerPayload, signWith) — but
	// the existing helper doesn't support ForkPayload yet. Build and
	// submit directly.
	if !forkApplyBroadcast(t, w, gkp.Public, "FORK parent", &pb.ForkPayload{
		NewGroupKey:  &pb.PublicKey{Raw: newGroupID[:]},
		NewStewards:  newStewardKeys,
		NewThreshold: 2,
	}, []crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Verify all hosts applied FORK: parent's root advanced.
	forkedParentRoot := w.Hosts()[0].State(gkp.Public).Root()
	if forkedParentRoot == parentRoot {
		t.Fatalf("FORK did not advance parent root (still %x)", parentRoot)
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != forkedParentRoot {
			t.Fatalf("parent root diverged after FORK: host %s=%x want %x", h.ID(), got, forkedParentRoot)
		}
	}
	t.Logf("parent root post-fork: %x", forkedParentRoot)

	// Step 2: Register the new group on all hosts.
	for _, h := range w.Hosts() {
		h.AddGroup(newGroupID, nil) // genesis transition will be applied separately
	}

	// Step 3: Apply CREATE_GROUP on the new group. Use a separate
	// helper that supports arbitrary group IDs (applyBroadcast is
	// hard-coded to gkp.Public).
	if !createGroupForBroadcast(t, w, newGroupID, "forked-vegas", "Forked Vegas Programmers",
		[]crypto.KeyPair{stewards[0], stewards[1]}, 2) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Verify new group has converged across all hosts.
	newRoot := w.Hosts()[0].State(newGroupID).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(newGroupID).Root(); got != newRoot {
			t.Fatalf("new group not converged: host %s=%x want %x", h.ID(), got, newRoot)
		}
	}
	t.Logf("new group root: %x", newRoot)

	// Step 4: DIVERGENCE — only the new group gets a CREATE_EVENT.
	if !applyBroadcastFor(t, w, newGroupID, "CREATE_EVENT (new group)",
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		&pb.CreateEventPayload{
			EventId:  "forked-event-1",
			Title:    "First Forked Meetup",
			StartsAt: nil,
			EndsAt:   nil,
			Capacity: 50,
		},
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Final convergence check: both groups must be at the same root
	// on all 4 hosts, and the two roots must be different (they
	// diverged).
	parentFinal := w.Hosts()[0].State(gkp.Public).Root()
	newFinal := w.Hosts()[0].State(newGroupID).Root()
	if parentFinal == newFinal {
		t.Fatalf("parent and new group have identical roots %x — divergence failed", parentFinal)
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentFinal {
			t.Errorf("parent root diverged across hosts: %s=%x want %x", h.ID(), got, parentFinal)
		}
		if got := h.State(newGroupID).Root(); got != newFinal {
			t.Errorf("new group root diverged across hosts: %s=%x want %x", h.ID(), got, newFinal)
		}
	}

	t.Logf("final parent root: %x", parentFinal)
	t.Logf("final new group root: %x", newFinal)
	t.Logf("host %s: parent log=%d, new group log=%d", w.Hosts()[0].ID(),
		len(w.Hosts()[0].State(gkp.Public).Log()),
		len(w.Hosts()[0].State(newGroupID).Log()))
}

// =============================================================================
// Helpers specific to the fork test
// =============================================================================

// applyBroadcastFor is like applyBroadcast but takes an explicit
// group ID instead of gkp.Public. Used for transitions on the new
// (forked) group.
func applyBroadcastFor(
	t *testing.T,
	w *sim.World,
	gid types.GroupID,
	label string,
	kind pb.TransitionType,
	innerPayload interface{},
	signWith []crypto.KeyPair,
) bool {
	t.Helper()

	h0 := w.Hosts()[0]
	st0 := h0.State(gid)
	if st0 == nil {
		t.Fatalf("%s: host %s has no state for group %x", label, h0.ID(), gid[:4])
		return false
	}
	prior := st0.Root()
	priorRoot := &pb.StateRoot{Hash: prior[:]}

	trProto := &pb.Transition{
		Type:       kind,
		PriorState: priorRoot,
	}
	if err := setTransitionPayload(trProto, innerPayload); err != nil {
		t.Fatalf("%s: %v", label, err)
		return false
	}

	canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
	if err != nil {
		t.Fatalf("%s: marshal canonical: %v", label, err)
		return false
	}
	sigs := make([]*pb.Signature, 0, len(signWith))
	for _, k := range signWith {
		s := crypto.Sign(k, gid, crypto.MsgKindTransition, canonical)
		sigs = append(sigs, &pb.Signature{Raw: s[:]})
	}
	trProto.StewardSignatures = &pb.Multisig{
		Threshold:  uint32(len(signWith)),
		Signatures: sigs,
	}

	tr, err := group.NewTransition(trProto, gid)
	if err != nil {
		t.Fatalf("%s: NewTransition: %v", label, err)
		return false
	}
	t.Logf("%s (gid=%x): prior=%x sigs=%d", label, gid[:4], priorRoot.GetHash()[:4], len(signWith))

	for _, h := range w.Hosts() {
		if _, err := h.SubmitTransition(gid, tr); err != nil {
			t.Fatalf("%s on host %s: %v", label, h.ID(), err)
			return false
		}
	}

	w.Advance(50 * time.Millisecond)
	newRoot := w.Hosts()[0].State(gid).Root()
	t.Logf("%s: post_root=%x", label, newRoot[:4])
	return true
}

// createGroupForBroadcast applies a CREATE_GROUP transition on the
// given group. Used to create the new (forked) group's genesis
// state.
func createGroupForBroadcast(
	t *testing.T,
	w *sim.World,
	gid types.GroupID,
	name, displayName string,
	initialStewards []crypto.KeyPair,
	threshold uint32,
) bool {
	t.Helper()
	stewardPBs := make([]*pb.PublicKey, len(initialStewards))
	for i, k := range initialStewards {
		stewardPBs[i] = &pb.PublicKey{Raw: k.Public[:]}
	}
	payload := &pb.CreateGroupPayload{
		CanonicalName:   name,
		DisplayName:     displayName,
		InitialStewards: stewardPBs,
		Threshold:       threshold,
	}
	return applyBroadcastFor(t, w, gid, "CREATE_GROUP (genesis)",
		pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP, payload, initialStewards)
}

// forkApplyBroadcast builds, signs, and broadcasts a FORK transition.
// Specialized because FORK applies to the parent group but declares a
// new child group; the helper reuses the parent's signing path.
func forkApplyBroadcast(
	t *testing.T,
	w *sim.World,
	parentID types.GroupID,
	label string,
	forkPayload *pb.ForkPayload,
	signWith []crypto.KeyPair,
) bool {
	t.Helper()
	return applyBroadcastFor(t, w, parentID, label,
		pb.TransitionType_TRANSITION_TYPE_FORK, forkPayload, signWith)
}

// setTransitionPayload wraps an inner payload into the Transition's
// payload oneof. Mirrors applyBroadcast's switch.
func setTransitionPayload(tr *pb.Transition, inner interface{}) error {
	switch p := inner.(type) {
	case *pb.CreateGroupPayload:
		tr.Payload = &pb.Transition_CreateGroup{CreateGroup: p}
	case *pb.AddStewardPayload:
		tr.Payload = &pb.Transition_AddSteward{AddSteward: p}
	case *pb.RemoveStewardPayload:
		tr.Payload = &pb.Transition_RemoveSteward{RemoveSteward: p}
	case *pb.ChangeThresholdPayload:
		tr.Payload = &pb.Transition_ChangeThreshold{ChangeThreshold: p}
	case *pb.AddMemberPayload:
		tr.Payload = &pb.Transition_AddMember{AddMember: p}
	case *pb.RemoveMemberPayload:
		tr.Payload = &pb.Transition_RemoveMember{RemoveMember: p}
	case *pb.CreateEventPayload:
		tr.Payload = &pb.Transition_CreateEvent{CreateEvent: p}
	case *pb.UpdateEventPayload:
		tr.Payload = &pb.Transition_UpdateEvent{UpdateEvent: p}
	case *pb.CancelEventPayload:
		tr.Payload = &pb.Transition_CancelEvent{CancelEvent: p}
	case *pb.RsvpPayload:
		tr.Payload = &pb.Transition_Rsvp{Rsvp: p}
	case *pb.ForkPayload:
		tr.Payload = &pb.Transition_Fork{Fork: p}
	case *pb.MigratePayload:
		tr.Payload = &pb.Transition_Migrate{Migrate: p}
	case *pb.BranchCreatePayload:
		tr.Payload = &pb.Transition_BranchCreate{BranchCreate: p}
	case *pb.IssueHostCertPayload:
		tr.Payload = &pb.Transition_IssueHostCert{IssueHostCert: p}
	case *pb.RevokeHostCertPayload:
		tr.Payload = &pb.Transition_RevokeHostCert{RevokeHostCert: p}
	case *pb.AttestPayload:
		tr.Payload = &pb.Transition_Attest{Attest: p}
	case *pb.SlashStewardPayload:
		tr.Payload = &pb.Transition_SlashSteward{SlashSteward: p}
	case *pb.DeclareStewardCustodyPayload:
		tr.Payload = &pb.Transition_DeclareStewardCustody{DeclareStewardCustody: p}
	case *pb.NameBindPayload:
		tr.Payload = &pb.Transition_NameBind{NameBind: p}
	case *pb.RemoveHostPeerPayload:
		tr.Payload = &pb.Transition_RemoveHostPeer{RemoveHostPeer: p}
	default:
		return &UnsupportedPayloadError{Type: inner}
	}
	return nil
}

// UnsupportedPayloadError is returned by setTransitionPayload when an
// inner payload type is not recognized.
type UnsupportedPayloadError struct {
	Type interface{}
}

func (e *UnsupportedPayloadError) Error() string {
	return "setTransitionPayload: unsupported inner payload type"
}