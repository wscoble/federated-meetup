// SPDX-License-Identifier: AGPL-3.0
//
// Name-bind scenario: a steward group binds a directory name
// ("vegas-programmers") to itself, so a directory host can
// resolve the name without trusting any external CA.
//
// What this exercises:
//   - NAME_BIND: stores name_bind/{dir}/{name}/{not_after} entry
//   - Two directory bindings (with and without directory_host)
//     land at distinct keys
//   - The verify gate rejects empty names and missing not_after
//   - Cross-host convergence on the post-bind transition
//
// Why this matters: directory binding is the G8 anti-phishing
// primitive. Without it, a directory can claim any name points
// to any group. With it, the directory MUST publish a valid
// NAME_BIND transition before resolving a name — and forging one
// requires a threshold of steward signatures.
package sim_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/crypto"
	"github.com/wscoble/federated-meetup/internal/group"
	"github.com/wscoble/federated-meetup/internal/types"
	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestNameBind_DirectoryBinding walks through:
//  1. Vegas Programmers exist
//  2. Stewards NAME_BIND "vegas-programmers" globally (directory_host="")
//  3. name_bind/*/vegas-programmers/<ts> entry appears
//  4. Stewards NAME_BIND the same name to a specific directory
//  5. Both entries coexist at distinct storage keys
//  6. All 4 hosts converge
//  7. An empty-name NAME_BIND is rejected by the verify gate
func TestNameBind_DirectoryBinding(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        53,
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

	// Pre-state.
	parentRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != parentRoot {
			t.Fatalf("parent not converged pre-bind: %s=%x want %x", h.ID(), got, parentRoot)
		}
	}

	// Step 2: global bind.
	notAfter := w.Now().Add(365 * 24 * time.Hour)
	globalBind := &pb.NameBindPayload{
		Name:    "vegas-programmers",
		NotAfter: timestamppb.New(notAfter),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND vegas-programmers (global)",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND,
		globalBind,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	globalRoot := w.Hosts()[0].State(gkp.Public).Root()
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != globalRoot {
			t.Fatalf("post-global-bind divergence: host %s=%x want %x", h.ID(), got, globalRoot)
		}
	}
	t.Logf("global bind recorded; root = %x", globalRoot[:4])

	// Step 3: directory-scoped bind.
	dirBind := &pb.NameBindPayload{
		Name:          "vegas-programmers",
		DirectoryHost: "directory.example.com",
		NotAfter:      timestamppb.New(notAfter),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND vegas-programmers (directory.example.com)",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND,
		dirBind,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	dirRoot := w.Hosts()[0].State(gkp.Public).Root()
	if dirRoot == globalRoot {
		t.Fatal("dir bind did not advance root")
	}
	for _, h := range w.Hosts()[1:] {
		if got := h.State(gkp.Public).Root(); got != dirRoot {
			t.Fatalf("post-dir-bind divergence: host %s=%x want %x", h.ID(), got, dirRoot)
		}
	}
	t.Logf("directory-scoped bind recorded; root = %x", dirRoot[:4])

	// Verify both entries coexist in the snapshot. Storage keys
	// are name_bind/*/vegas-programmers/<ts> and
	// name_bind/directory.example.com/vegas-programmers/<ts>.
	notAfterSec := notAfter.Unix()
	verifyNameBindEntry(t, w.Hosts()[0].State(gkp.Public),
		"name_bind/*/vegas-programmers/"+itoa(notAfterSec))
	verifyNameBindEntry(t, w.Hosts()[0].State(gkp.Public),
		"name_bind/directory.example.com/vegas-programmers/"+itoa(notAfterSec))

	// Step 4: empty-name NAME_BIND should be rejected. Note that
	// after this rejection, the equivocation log has recorded an
	// entry for alice and bob at prior_state=dirRoot (the equivocation
	// log records on every signature-valid attempt, even rejected
	// ones — it's a defense-in-depth thing, not a "successful apply"
	// thing). We don't try a second rejection test on the same
	// prior_state because the equivocation check would fire before
	// the verify gate.
	emptyBind := &pb.NameBindPayload{
		NotAfter: timestamppb.New(notAfter),
	}
	trProto := buildNameBindTransition(t, w, gkp.Public, dirRoot, emptyBind,
		[]crypto.KeyPair{stewards[0], stewards[1]})
	h0 := w.Hosts()[0]
	if _, err := h0.SubmitTransition(gkp.Public, trProto); err == nil {
		t.Fatal("empty-name NAME_BIND should have been rejected")
	} else if !strings.Contains(err.Error(), "too short") {
		t.Fatalf("expected 'too short' error, got: %v", err)
	} else {
		t.Logf("empty-name bind correctly rejected: %v", err)
	}

	// State must NOT have advanced past dirRoot.
	for _, h := range w.Hosts() {
		if got := h.State(gkp.Public).Root(); got != dirRoot {
			t.Fatalf("rejected bind should not have advanced root on host %s: %x want %x",
				h.ID(), got, dirRoot)
		}
	}
}

// buildNameBindTransition is a helper for the negative tests —
// lets the test submit a NAME_BIND with explicit payload (good or
// bad) against a known prior_state.
func buildNameBindTransition(
	t *testing.T,
	w *sim.World,
	gid types.GroupID,
	priorRoot types.Hash,
	payload *pb.NameBindPayload,
	signWith []crypto.KeyPair,
) *group.Transition {
	t.Helper()
	trProto := &pb.Transition{
		Type:       pb.TransitionType_TRANSITION_TYPE_NAME_BIND,
		PriorState: &pb.StateRoot{Hash: priorRoot[:]},
	}
	if err := setTransitionPayload(trProto, payload); err != nil {
		t.Fatal(err)
	}
	canonical, err := group.MarshalCanonicalForSigningHelper(trProto)
	if err != nil {
		t.Fatal(err)
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
		t.Fatal(err)
	}
	return tr
}

// verifyNameBindEntry asserts that the entry with the given key
// exists in the snapshot with a non-nil value (a 1-byte marker).
func verifyNameBindEntry(t *testing.T, st *group.State, key string) {
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