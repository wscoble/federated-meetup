// SPDX-License-Identifier: MIT
//
// Cycle 38: NAME_BIND lookup-key semantics.
//
// NAME_BIND (group.go:832) writes a single byte (0x01) to a storage
// key derived from the (directory_host, name, not_after) triple:
//
//     nameBindStorageKey(p) = "name_bind/<dir>/<name>/<not_after_secs>"
//
// What's pinned down here:
//
//   1. Two NAME_BINDs for the same (dir, name, not_after) on the same
//      group BOTH SUCCEED. There's no uniqueness check. The second
//      is a no-op for the entry value (writes []byte{1} over []byte{1})
//      but Seq++ advances the root — same as ADD_STEWARD duplicate
//      (cycle 33).
//
//   2. Two NAME_BINDs for the same (dir, name) but DIFFERENT not_after
//      values create DISTINCT entries. The KV grows linearly with
//      rebinding windows. There's no cleanup mechanism — old bindings
//      linger forever.
//
//   3. Two NAME_BINDs for the same name but DIFFERENT directories
//      create DISTINCT entries (per-directory scoping).
//
// Why this matters: NAME_BIND is the directory-lookup primitive. If
// the protocol doesn't enforce "one canonical bind per (dir, name),
// most-recent not_after wins," clients get ambiguous results. Today
// the answer is "latest by not_after" — but the protocol doesn't
// codify that. Tests pin current behavior; future code can add
// "name-already-bound-with-this-not_after" rejection if desired.
package sim_test

import (
	"testing"
	"time"

	"github.com/sscoble/federated-meetup/internal/crypto"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNameBind_DuplicateAccepted_NoRootAdvanceBySeq(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        102,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	parentRoot := w.Hosts()[0].State(gkp.Public).Root()

	notAfter := w.Now().Add(365 * 24 * time.Hour)
	nameBind := &pb.NameBindPayload{
		DirectoryHost: "directory.example.com",
		Name:          "vegas-programmers",
		NotAfter:      timestamppb.New(notAfter),
	}

	// First bind.
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND #1",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND, nameBind,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	rootAfterFirst := w.Hosts()[0].State(gkp.Public).Root()
	if rootAfterFirst == parentRoot {
		t.Fatal("first NAME_BIND did not advance root")
	}

	// Second bind — identical (dir, name, not_after).
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND #2 (duplicate)",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND, nameBind,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	rootAfterSecond := w.Hosts()[0].State(gkp.Public).Root()

	// Root advances (Seq++) even though the entry value is unchanged.
	if rootAfterSecond == rootAfterFirst {
		t.Fatal("duplicate NAME_BIND did not advance root — Seq counter may not be in Merkle leaf")
	}

	t.Logf("duplicate NAME_BIND accepted, root advanced via Seq++")
}

func TestNameBind_DifferentNotAfter_CreatesDistinctEntries(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        103,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	dir := "directory.example.com"
	name := "vegas-programmers"

	// Bind with not_after = T+30d.
	notAfter1 := w.Now().Add(30 * 24 * time.Hour)
	bind1 := &pb.NameBindPayload{
		DirectoryHost: dir,
		Name:          name,
		NotAfter:      timestamppb.New(notAfter1),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND T+30d",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND, bind1,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	rootAfterFirst := w.Hosts()[0].State(gkp.Public).Root()

	// Bind with not_after = T+60d (same dir, same name, different window).
	notAfter2 := w.Now().Add(60 * 24 * time.Hour)
	bind2 := &pb.NameBindPayload{
		DirectoryHost: dir,
		Name:          name,
		NotAfter:      timestamppb.New(notAfter2),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND T+60d",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND, bind2,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)
	rootAfterSecond := w.Hosts()[0].State(gkp.Public).Root()
	if rootAfterSecond == rootAfterFirst {
		t.Fatal("second NAME_BIND did not advance root")
	}

	// Both entries should be present in the snapshot (different not_after keys).
	entries := w.Hosts()[0].State(gkp.Public).Snapshot().Entries
	var found1, found2 bool
	for _, e := range entries {
		if len(e.Key) >= 10 && e.Key[:10] == "name_bind/" {
			if len(e.Value) == 0 || e.Value[0] != 1 {
				continue
			}
			// Check if the not_after timestamp appears in the key.
			if containsString(e.Key, itoa(notAfter1.Unix())) {
				found1 = true
			}
			if containsString(e.Key, itoa(notAfter2.Unix())) {
				found2 = true
			}
		}
	}
	if !found1 {
		t.Errorf("missing NAME_BIND entry for T+30d (key should contain %d)", notAfter1.Unix())
	}
	if !found2 {
		t.Errorf("missing NAME_BIND entry for T+60d (key should contain %d)", notAfter2.Unix())
	}
	t.Logf("two distinct NAME_BIND entries coexist (different not_after windows)")
}

func TestNameBind_DifferentDirectories_CreatesDistinctEntries(t *testing.T) {
	w, _ := sim.NewWorld(sim.Config{
		Seed:        104,
		HostCount:   4,
		InitialTime: time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
	})
	defer w.Close()
	w.AttachMesh(sim.NewMesh(w, sim.DDILBenign))

	gkp := setupVegasProgrammers(w)
	stewards := stewardKPsForTest(w)

	notAfter := w.Now().Add(365 * 24 * time.Hour)

	bindDirA := &pb.NameBindPayload{
		DirectoryHost: "directory-a.example.com",
		Name:          "vegas-programmers",
		NotAfter:      timestamppb.New(notAfter),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND dirA",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND, bindDirA,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)

	bindDirB := &pb.NameBindPayload{
		DirectoryHost: "directory-b.example.com",
		Name:          "vegas-programmers",
		NotAfter:      timestamppb.New(notAfter),
	}
	if !applyBroadcastFor(t, w, gkp.Public, "NAME_BIND dirB",
		pb.TransitionType_TRANSITION_TYPE_NAME_BIND, bindDirB,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		return
	}
	w.Advance(50 * time.Millisecond)

	// Both entries should exist (different directory hosts).
	entries := w.Hosts()[0].State(gkp.Public).Snapshot().Entries
	var foundA, foundB bool
	for _, e := range entries {
		if len(e.Key) >= 10 && e.Key[:10] == "name_bind/" {
			if len(e.Value) == 0 || e.Value[0] != 1 {
				continue
			}
			if containsString(e.Key, "directory-a.example.com") {
				foundA = true
			}
			if containsString(e.Key, "directory-b.example.com") {
				foundB = true
			}
		}
	}
	if !foundA {
		t.Error("missing NAME_BIND entry for directory-a.example.com")
	}
	if !foundB {
		t.Error("missing NAME_BIND entry for directory-b.example.com")
	}
	t.Logf("same name on two directories creates two distinct entries")
}

// containsString reports whether sub appears anywhere in s.
func containsString(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}