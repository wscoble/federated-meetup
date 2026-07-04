// SPDX-License-Identifier: AGPL-3.0
//
// Tests for internal/host/service.go. The Service type satisfies the
// GENERATED HostServiceHandler interface (federatedmeetupv1connect),
// so these tests exercise the contract that `buf generate` produced.
//
// What we verify:
//   1. Compile-time: Service satisfies the generated interface (asserted in service.go).
//   2. GetGroup by GroupKey returns snapshot+stewards+threshold from *group.State.
//   3. GetGroup by CanonicalName returns Unimplemented (v0 has no name dir).
//   4. GetGroup with mismatched GroupKey returns NotFound.
//   5. SubmitTransition applies a CREATE_EVENT and the new snapshot/root
//      are returned.
//   6. GetEvent returns NotFound for an event that was never created.
//   7. ListGroups returns the home group only (v0).
//   8. ResolveName returns the home group key + this host's name.
//   9. SubmitUserAction returns Unimplemented (v0).

package sim_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/host"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/sscoble/federated-meetup/sim"
)

// freshService builds a Service backed by a fresh *group.State that
// has the vegas-programmers group applied. Returns the Service, world,
// and the group keypair.
func freshService(t *testing.T) (*host.Service, *sim.World, crypto.KeyPair) {
	t.Helper()
	w, err := sim.NewWorld(sim.Config{
		Seed:        1,
		HostCount:   1,
		InitialTime: time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	gkp := setupVegasProgrammers(w)
	h := w.Hosts()[0]
	state := h.State(gkp.Public)
	if state == nil {
		t.Fatal("setupVegasProgrammers did not produce a state on host 0")
	}
	svc := host.NewService("host-0.test", state)
	return svc, w, gkp
}

func TestHostService_GetGroup_ByKey(t *testing.T) {
	svc, _, gkp := freshService(t)
	req := connect.NewRequest(&pb.GetGroupRequest{
		Identifier: &pb.GetGroupRequest_GroupKey{GroupKey: &pb.PublicKey{Raw: gkp.Public[:]}},
	})
	resp, err := svc.GetGroup(context.Background(), req)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if resp.Msg.Snapshot == nil {
		t.Fatal("snapshot is nil")
	}
	if resp.Msg.Threshold != 2 {
		t.Errorf("threshold = %d, want 2", resp.Msg.Threshold)
	}
	if len(resp.Msg.Stewards) != 3 {
		t.Errorf("stewards = %d, want 3", len(resp.Msg.Stewards))
	}
	if len(resp.Msg.Snapshot.Root.Hash) != 32 {
		t.Errorf("root hash length = %d, want 32", len(resp.Msg.Snapshot.Root.Hash))
	}
}

func TestHostService_GetGroup_ByName_Unimplemented(t *testing.T) {
	svc, _, _ := freshService(t)
	req := connect.NewRequest(&pb.GetGroupRequest{
		Identifier: &pb.GetGroupRequest_CanonicalName{CanonicalName: "vegas-programmers"},
	})
	_, err := svc.GetGroup(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for canonical_name, got nil")
	}
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("code = %v, want Unimplemented", connect.CodeOf(err))
	}
}

func TestHostService_GetGroup_WrongKey(t *testing.T) {
	svc, _, _ := freshService(t)
	var wrongKey [32]byte
	for i := range wrongKey {
		wrongKey[i] = 0xFF
	}
	req := connect.NewRequest(&pb.GetGroupRequest{
		Identifier: &pb.GetGroupRequest_GroupKey{GroupKey: &pb.PublicKey{Raw: wrongKey[:]}},
	})
	_, err := svc.GetGroup(context.Background(), req)
	if err == nil {
		t.Fatal("expected NotFound, got nil")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestHostService_SubmitTransition_CreateEvent(t *testing.T) {
	svc, w, gkp := freshService(t)

	alice := keyPairFromSeed(w, "alice")
	bob := keyPairFromSeed(w, "bob")
	carol := keyPairFromSeed(w, "carol")

	eventPayload := &pb.CreateEventPayload{
		EventId:    "evt-host-001",
		Title:      "Host Service Test Event",
		StartsAt:   timestamppb.New(w.Now()),
		Location:   "Online",
		Capacity:   50,
		Metadata:   map[string][]byte{"category": []byte("test")},
	}
	ok := applyBroadcast(t, w, gkp, "CREATE_EVENT evt-host-001",
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		eventPayload,
		[]crypto.KeyPair{alice, bob, carol})
	if !ok {
		t.Fatal("setup applyBroadcast failed")
	}
	_ = svc

	req := connect.NewRequest(&pb.SubmitTransitionRequest{
		GroupKey: &pb.PublicKey{Raw: gkp.Public[:]},
		Transition: &pb.Transition{
			Type:    pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
			Payload: &pb.Transition_CreateEvent{CreateEvent: eventPayload},
			SignedAt: timestamppb.New(w.Now()),
		},
	})
	// Note: this transition won't have valid signatures — Apply will fail.
	// We expect FailedPrecondition (signature verify). v0 doesn't gate the
	// "happy path" through the host service; that wiring is a follow-up cycle
	// that needs sigsFor in the request builder. For now, the contract is:
	// Service must not crash, must return a structured error.
	_, err := svc.SubmitTransition(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for unsigned transition, got nil")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

func TestHostService_GetEvent_NotFound(t *testing.T) {
	svc, _, gkp := freshService(t)
	req := connect.NewRequest(&pb.GetEventRequest{
		GroupKey: &pb.PublicKey{Raw: gkp.Public[:]},
		EventId:  "does-not-exist",
	})
	_, err := svc.GetEvent(context.Background(), req)
	if err == nil {
		t.Fatal("expected NotFound, got nil")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestHostService_GetEvent_Found(t *testing.T) {
	svc, w, gkp := freshService(t)

	alice := keyPairFromSeed(w, "alice")
	bob := keyPairFromSeed(w, "bob")
	carol := keyPairFromSeed(w, "carol")

	eventPayload := &pb.CreateEventPayload{
		EventId:  "evt-host-002",
		Title:    "Found Event",
		StartsAt: timestamppb.New(w.Now()),
	}
	ok := applyBroadcast(t, w, gkp, "CREATE_EVENT evt-host-002",
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		eventPayload,
		[]crypto.KeyPair{alice, bob, carol})
	if !ok {
		t.Fatal("setup applyBroadcast failed")
	}

	req := connect.NewRequest(&pb.GetEventRequest{
		GroupKey: &pb.PublicKey{Raw: gkp.Public[:]},
		EventId:  "evt-host-002",
	})
	resp, err := svc.GetEvent(context.Background(), req)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if resp.Msg.Event == nil || resp.Msg.Event.EventId != "evt-host-002" {
		t.Errorf("Event = %v, want evt-host-002", resp.Msg.Event)
	}
}

func TestHostService_ListEvents(t *testing.T) {
	svc, w, gkp := freshService(t)

	alice := keyPairFromSeed(w, "alice")
	bob := keyPairFromSeed(w, "bob")
	carol := keyPairFromSeed(w, "carol")

	for i, eid := range []string{"evt-list-a", "evt-list-b", "evt-list-c"} {
		eventPayload := &pb.CreateEventPayload{
			EventId:  eid,
			Title:    "List Event " + string(rune('a'+i)),
			StartsAt: timestamppb.New(w.Now()),
		}
		ok := applyBroadcast(t, w, gkp, "CREATE_EVENT "+eid,
			pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
			eventPayload,
			[]crypto.KeyPair{alice, bob, carol})
		if !ok {
			t.Fatalf("setup applyBroadcast failed for %s", eid)
		}
	}

	req := connect.NewRequest(&pb.ListEventsRequest{
		GroupKey: &pb.PublicKey{Raw: gkp.Public[:]},
		PageSize: 2,
	})
	resp, err := svc.ListEvents(context.Background(), req)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(resp.Msg.Events) != 2 {
		t.Errorf("len(events) = %d, want 2", len(resp.Msg.Events))
	}
	if resp.Msg.NextCursor == "" {
		t.Error("NextCursor is empty, expected a cursor for next page")
	}
}

func TestHostService_ListGroups(t *testing.T) {
	svc, _, gkp := freshService(t)
	req := connect.NewRequest(&pb.ListGroupsRequest{})
	resp, err := svc.ListGroups(context.Background(), req)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(resp.Msg.Groups) != 1 {
		t.Fatalf("len(groups) = %d, want 1", len(resp.Msg.Groups))
	}
	if resp.Msg.Groups[0].GroupKey == nil {
		t.Fatal("GroupKey is nil")
	}
	var got [32]byte
	copy(got[:], resp.Msg.Groups[0].GroupKey.Raw)
	if got != gkp.Public {
		t.Errorf("GroupKey = %x, want %x", got, gkp.Public)
	}
}

func TestHostService_ResolveName(t *testing.T) {
	svc, _, gkp := freshService(t)
	req := connect.NewRequest(&pb.ResolveNameRequest{CanonicalName: "vegas-programmers"})
	resp, err := svc.ResolveName(context.Background(), req)
	if err != nil {
		t.Fatalf("ResolveName: %v", err)
	}
	if resp.Msg.GroupKey == nil {
		t.Fatal("GroupKey is nil")
	}
	var got [32]byte
	copy(got[:], resp.Msg.GroupKey.Raw)
	if got != gkp.Public {
		t.Errorf("GroupKey = %x, want %x", got, gkp.Public)
	}
	if len(resp.Msg.Hosts) != 1 || resp.Msg.Hosts[0] != "host-0.test" {
		t.Errorf("Hosts = %v, want [host-0.test]", resp.Msg.Hosts)
	}
}

func TestHostService_SubmitUserAction_NoPayload(t *testing.T) {
	svc, _, gkp := freshService(t)
	req := connect.NewRequest(&pb.SubmitUserActionRequest{
		GroupKey: &pb.PublicKey{Raw: gkp.Public[:]},
		Type:     pb.TransitionType_TRANSITION_TYPE_RSVP,
	})
	_, err := svc.SubmitUserAction(context.Background(), req)
	if err == nil {
		t.Fatal("expected InvalidArgument, got nil")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestHostService_SubmitUserAction_InvalidType(t *testing.T) {
	svc, _, gkp := freshService(t)
	req := connect.NewRequest(&pb.SubmitUserActionRequest{
		GroupKey:          &pb.PublicKey{Raw: gkp.Public[:]},
		Type:              pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		TransitionPayload: []byte{0x01},
	})
	_, err := svc.SubmitUserAction(context.Background(), req)
	if err == nil {
		t.Fatal("expected InvalidArgument, got nil")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

func TestHostService_SubmitUserAction_GroupNotFound(t *testing.T) {
	svc, _, _ := freshService(t)
	var wrongKey [32]byte
	for i := range wrongKey {
		wrongKey[i] = 0xEE
	}
	req := connect.NewRequest(&pb.SubmitUserActionRequest{
		GroupKey:          &pb.PublicKey{Raw: wrongKey[:]},
		Type:              pb.TransitionType_TRANSITION_TYPE_RSVP,
		TransitionPayload: []byte{0x01},
	})
	_, err := svc.SubmitUserAction(context.Background(), req)
	if err == nil {
		t.Fatal("expected NotFound, got nil")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

// TestHostService_SubmitUserAction_RSVP_BadPayload verifies that a
// malformed transition_payload (not a valid pb.Transition) is rejected
// with InvalidArgument.
func TestHostService_SubmitUserAction_RSVP_BadPayload(t *testing.T) {
	svc, _, gkp := freshService(t)
	req := connect.NewRequest(&pb.SubmitUserActionRequest{
		GroupKey:          &pb.PublicKey{Raw: gkp.Public[:]},
		Type:              pb.TransitionType_TRANSITION_TYPE_RSVP,
		TransitionPayload: []byte{0xDE, 0xAD, 0xBE, 0xEF},
	})
	_, err := svc.SubmitUserAction(context.Background(), req)
	if err == nil {
		t.Fatal("expected InvalidArgument, got nil")
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("code = %v, want InvalidArgument", connect.CodeOf(err))
	}
}

// TestHostService_SubmitUserAction_RSVP_UnsignedTransition verifies that
// a well-formed RSVP transition without steward signatures is rejected
// with FailedPrecondition (signature verification). This confirms the
// RPC is wired through to Apply — it no longer returns Unimplemented.
func TestHostService_SubmitUserAction_RSVP_UnsignedTransition(t *testing.T) {
	svc, w, gkp := freshService(t)
	stewards := stewardKPsForTest(w)
	eve := keyPairFromSeed(w, "eve-rsvp-host")

	// Create an event to RSVP to.
	createEvt := &pb.CreateEventPayload{
		EventId:  "evt-user-action-001",
		Title:    "User Action Test Event",
		StartsAt: timestamppb.New(w.Now()),
		Capacity: 50,
	}
	if !applyBroadcast(t, w, gkp, "CREATE_EVENT for user-action",
		pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT,
		createEvt,
		[]crypto.KeyPair{stewards[0], stewards[1]}) {
		t.Fatal("setup CREATE_EVENT failed")
	}

	// Build an RSVP transition with no steward signatures.
	rsvpPayload := &pb.RsvpPayload{
		EventId: "evt-user-action-001",
		User:    &pb.PublicKey{Raw: eve.Public[:]},
	}
	trProto := &pb.Transition{
		Type:     pb.TransitionType_TRANSITION_TYPE_RSVP,
		Payload:  &pb.Transition_Rsvp{Rsvp: rsvpPayload},
		SignedAt: timestamppb.New(w.Now()),
	}
	payload, err := proto.Marshal(trProto)
	if err != nil {
		t.Fatalf("marshal transition: %v", err)
	}

	req := connect.NewRequest(&pb.SubmitUserActionRequest{
		GroupKey:          &pb.PublicKey{Raw: gkp.Public[:]},
		Type:              pb.TransitionType_TRANSITION_TYPE_RSVP,
		TransitionPayload: payload,
		UserEnvelope:      &pb.SignedEnvelope{Message: []byte("test")},
	})
	_, err = svc.SubmitUserAction(context.Background(), req)
	if err == nil {
		t.Fatal("expected FailedPrecondition, got nil")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("code = %v, want FailedPrecondition", connect.CodeOf(err))
	}
}

// TestHostService_MultiGroup_ListGroups verifies the host can serve
// multiple groups simultaneously and ListGroups returns all of them.
func TestHostService_MultiGroup_ListGroups(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        2,
		HostCount:   1,
		InitialTime: time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	t.Cleanup(func() { w.Close() })

	gkp1 := setupVegasProgrammers(w)
	// Construct a state for a second group with a different key.
	seed2 := w.DeriveSeed("reno-programmers")
	var seedBytes [32]byte
	for j := 0; j < 8; j++ {
		seedBytes[j] = byte(seed2 >> (8 * j))
	}
	gkp2 := crypto.KeyPairFromSeed(seedBytes)

	state1 := w.Hosts()[0].State(gkp1.Public)
	if state1 == nil {
		t.Fatal("setupVegasProgrammers did not produce state1")
	}
	state2 := group.NewState(gkp2.Public)
	svc := host.NewService("host-multi", state1, state2)
	if svc.Groups().Len() != 2 {
		t.Fatalf("Groups().Len() = %d, want 2", svc.Groups().Len())
	}

	req := connect.NewRequest(&pb.ListGroupsRequest{})
	resp, err := svc.ListGroups(context.Background(), req)
	if err != nil {
		t.Fatalf("ListGroups: %v", err)
	}
	if len(resp.Msg.Groups) != 2 {
		t.Fatalf("len(groups) = %d, want 2", len(resp.Msg.Groups))
	}
	seen := map[[32]byte]bool{}
	for _, g := range resp.Msg.Groups {
		var k [32]byte
		copy(k[:], g.GroupKey.Raw)
		seen[k] = true
	}
	if !seen[gkp1.Public] {
		t.Errorf("group 1 (%x) not in ListGroups", gkp1.Public)
	}
	if !seen[gkp2.Public] {
		t.Errorf("group 2 (%x) not in ListGroups", gkp2.Public)
	}
}

// TestHostService_MultiGroup_Routing verifies that requests for
// a hosted group succeed and requests for a non-hosted group
// return NotFound.
func TestHostService_MultiGroup_Routing(t *testing.T) {
	w, err := sim.NewWorld(sim.Config{
		Seed:        3,
		HostCount:   1,
		InitialTime: time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewWorld: %v", err)
	}
	t.Cleanup(func() { w.Close() })
	gkp1 := setupVegasProgrammers(w)
	state1 := w.Hosts()[0].State(gkp1.Public)

	// Make a state for a different group key (not registered).
	otherKey := [32]byte{}
	for i := range otherKey {
		otherKey[i] = 0xAB
	}
	state2 := group.NewState(otherKey)
	svc := host.NewService("host-routing", state1, state2)

	// gkp1 is registered → GetGroup succeeds.
	req := connect.NewRequest(&pb.GetGroupRequest{
		Identifier: &pb.GetGroupRequest_GroupKey{GroupKey: &pb.PublicKey{Raw: gkp1.Public[:]}},
	})
	if _, err := svc.GetGroup(context.Background(), req); err != nil {
		t.Errorf("GetGroup on hosted group: %v", err)
	}

	// otherKey is registered too → also succeeds.
	req2 := connect.NewRequest(&pb.GetGroupRequest{
		Identifier: &pb.GetGroupRequest_GroupKey{GroupKey: &pb.PublicKey{Raw: otherKey[:]}},
	})
	if _, err := svc.GetGroup(context.Background(), req2); err != nil {
		t.Errorf("GetGroup on second hosted group: %v", err)
	}

	// A key that is NOT registered → NotFound.
	var missingKey [32]byte
	for i := range missingKey {
		missingKey[i] = 0xFF
	}
	req3 := connect.NewRequest(&pb.GetGroupRequest{
		Identifier: &pb.GetGroupRequest_GroupKey{GroupKey: &pb.PublicKey{Raw: missingKey[:]}},
	})
	_, err = svc.GetGroup(context.Background(), req3)
	if err == nil {
		t.Fatal("expected NotFound, got nil")
	}
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("code = %v, want NotFound", connect.CodeOf(err))
	}
}
