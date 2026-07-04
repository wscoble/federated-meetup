// SPDX-License-Identifier: AGPL-3.0
//
// Multi-group scenario: two distinct groups (Vegas Programmers
// and Tucson Designers) running on the same set of 4 hosts.
// Each group applies its own transitions independently.
//
// What this exercises:
//   - State machine disambiguates by GroupID — Vegas transitions
//     don't accidentally apply to Tucson and vice versa
//   - Cross-host convergence per group is INDEPENDENT — Vegas
//     converges to its root, Tucson converges to its root, and
//     the two roots can diverge arbitrarily
//   - Each host's steward set per group is correct
//
// Why this matters: federation means many groups on the same
// infrastructure. Hosts serve multiple groups simultaneously;
// state isolation per GroupID is foundational.
package sim_test

import (
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	"github.com/wscoble/federated-meetup/internal/hlc"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
)

// TestMultiGroup_TwoGroupsOnSameHosts walks through:
//  1. Vegas Programmers exist on all 4 hosts (group A)
//  2. Tucson Designers are created on all 4 hosts (group B)
//  3. Each group independently applies a CHANGE_THRESHOLD transition
//  4. Both groups converge within themselves, but to DIFFERENT roots
//  5. Each group's steward sets are independent
func TestMultiGroup_TwoGroupsOnSameHosts(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        47,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	mesh := sim.NewMesh(w, sim.DDILBenign)
	w.AttachMesh(mesh)

	// Step 1: Vegas Programmers exist.
	gkpA := setupVegasProgrammers(w) // group A: 3 stewards, threshold 2
	stewardsA := stewardKPsForTest(w)

	// Step 2: Create Tucson Designers (group B) on all 4 hosts.
	// Different name + different group key. 3 stewards, threshold 2.
	gkpB := setupTucsonDesigners(w, "tucson-designers", "Tucson Designers")

	// Sanity: both groups exist on all hosts, with different roots.
	rootA := w.Hosts()[0].State(gkpA.Public).Root()
	rootB := w.Hosts()[0].State(gkpB.Public).Root()
	if rootA == rootB {
		t.Fatalf("groups A and B have identical roots %x — should differ", rootA)
	}
	t.Logf("group A (Vegas) root: %x", rootA[:4])
	t.Logf("group B (Tucson) root: %x", rootB[:4])

	// Each host should have BOTH groups.
	for _, h := range w.Hosts() {
		if got := h.State(gkpA.Public).Root(); got != rootA {
			t.Errorf("host %s: Vegas root = %x, want %x", h.ID(), got, rootA)
		}
		if got := h.State(gkpB.Public).Root(); got != rootB {
			t.Errorf("host %s: Tucson root = %x, want %x", h.ID(), got, rootB)
		}
	}

	// Step 3: Each group applies an independent CHANGE_THRESHOLD.
	if !applyBroadcastFor(t, w, gkpA.Public, "Vegas CHANGE_THRESHOLD 3",
		pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		&pb.ChangeThresholdPayload{NewThreshold: 3},
		[]crypto.KeyPair{stewardsA[0], stewardsA[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Tucson stewards (different KPs derived from "designer-a/b/c").
	// We rebuild them via the same setup that produced them.
	stewardsB := setupTucsonStewards(w, "tucson-designers")
	if !applyBroadcastFor(t, w, gkpB.Public, "Tucson CHANGE_THRESHOLD 3",
		pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD,
		&pb.ChangeThresholdPayload{NewThreshold: 3},
		[]crypto.KeyPair{stewardsB[0], stewardsB[1]}) {
		return
	}
	w.Advance(500 * time.Millisecond)

	// Step 4: convergence check.
	newRootA := w.Hosts()[0].State(gkpA.Public).Root()
	newRootB := w.Hosts()[0].State(gkpB.Public).Root()
	if newRootA == rootA {
		t.Fatalf("Vegas root did not advance")
	}
	if newRootB == rootB {
		t.Fatalf("Tucson root did not advance")
	}
	if newRootA == newRootB {
		t.Fatalf("Vegas and Tucson converged to the SAME root %x — state isolation broken", newRootA)
	}
	t.Logf("group A post-change: %x", newRootA[:4])
	t.Logf("group B post-change: %x", newRootB[:4])

	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkpA.Public).Root(); got != newRootA {
			t.Errorf("host %s: Vegas root divergence: got %x want %x", h.ID(), got, newRootA)
		}
		if got := h.State(gkpB.Public).Root(); got != newRootB {
			t.Errorf("host %s: Tucson root divergence: got %x want %x", h.ID(), got, newRootB)
		}
	}

	// Step 5: steward-set independence. Vegas has alice/bob/carol
	// (from setupVegasProgrammers). Tucson has designer-a/b/c.
	if got := len(w.Hosts()[0].State(gkpA.Public).Stewards()); got != 3 {
		t.Errorf("Vegas steward count = %d, want 3", got)
	}
	if got := len(w.Hosts()[0].State(gkpB.Public).Stewards()); got != 3 {
		t.Errorf("Tucson steward count = %d, want 3", got)
	}
}

// setupTucsonDesigners creates a 3-steward, threshold-2 group named
// "tucson-designers" on every host in the world. Returns the group's
// keypair. Mirrors setupVegasProgrammers but with different labels.
func setupTucsonDesigners(w *sim.World, name, displayName string) crypto.KeyPair {
	labels := []string{"designer-a", "designer-b", "designer-c"}
	seeds := make([]uint64, len(labels))
	for i, l := range labels {
		seeds[i] = w.DeriveSeed(l)
	}
	stewards := make([]crypto.KeyPair, len(labels))
	for i, s := range seeds {
		var seed [32]byte
		for j := 0; j < 8; j++ {
			seed[j] = byte(s >> (8 * j))
		}
		stewards[i] = crypto.KeyPairFromSeed(seed)
	}
	var groupSeed [32]byte
	gid := w.DeriveSeed(name)
	for j := 0; j < 8; j++ {
		groupSeed[j] = byte(gid >> (8 * j))
	}
	groupKP := crypto.KeyPairFromSeed(groupSeed)

	stewardPBList := stewardPBs(stewards)
	payload := &pb.CreateGroupPayload{
		CanonicalName:   name,
		DisplayName:     displayName,
		InitialStewards: stewardPBList,
		Threshold:       2,
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(&pb.Transition{
		Type:    pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload: &pb.Transition_CreateGroup{CreateGroup: payload},
	})
	if err != nil {
		panic(err)
	}
	multisig := &pb.Multisig{
		Threshold:  2,
		Signatures: sigsFor(stewards[:2], groupKP.Public, canonical),
	}
	tr, err := group.NewTransition(&pb.Transition{
		Type:              pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP,
		Payload:           &pb.Transition_CreateGroup{CreateGroup: payload},
		StewardSignatures: multisig,
	}, groupKP.Public)
	if err != nil {
		panic(err)
	}

	for _, h := range w.Hosts() {
		h.AddGroup(groupKP.Public, tr)
		if _, err := h.SubmitTransition(groupKP.Public, tr); err != nil {
			panic(err)
		}
	}
	return groupKP
}

// setupTucsonStewards derives the same steward keypairs
// setupTucsonDesigners used for the given world. Used by tests
// that need to sign transitions on behalf of Tucson's stewards.
func setupTucsonStewards(w *sim.World, groupLabel string) []crypto.KeyPair {
	labels := []string{"designer-a", "designer-b", "designer-c"}
	out := make([]crypto.KeyPair, len(labels))
	for i, l := range labels {
		s := w.DeriveSeed(l)
		var seed [32]byte
		for j := 0; j < 8; j++ {
			seed[j] = byte(s >> (8 * j))
		}
		out[i] = crypto.KeyPairFromSeed(seed)
	}
	return out
}

// hlcBytes is a tiny shim — referenced indirectly via hlc.New
// inside applyBroadcastFor. Kept as a sanity placeholder so the
// import isn't accidentally dropped.
var _ = hlc.New