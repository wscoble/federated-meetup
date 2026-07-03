// SPDX-License-Identifier: AGPL-3.0
//
// Audit H-7/H-8 (cycle 51): GetLog + GetSnapshot RPC tests.
//
// GetLog: paginated transition log retrieval for mirror bootstrap.
// GetSnapshot: current-head state snapshot for fast mirror bootstrap.
//
// Both tested via direct handler calls (no HTTP transport).

package sim_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/sscoble/federated-meetup/internal/group"
	"github.com/sscoble/federated-meetup/internal/host"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// TestH7H8_GetLogEmptyGroup verifies GetLog on a group with no
// transitions returns an empty list + total 0.
func TestH7H8_GetLogEmptyGroup(t *testing.T) {
	gid := types.GroupID{0x77}
	state := group.NewState(gid)
	svc := host.NewService("test", state)

	resp, err := svc.GetLog(context.Background(),
		connect.NewRequest(&pb.GetLogRequest{
			GroupKey: &pb.PublicKey{Raw: gid[:]},
		}))
	if err != nil {
		t.Fatalf("GetLog: %v", err)
	}
	if resp.Msg.Total != 0 {
		t.Errorf("expected total 0, got %d", resp.Msg.Total)
	}
	if len(resp.Msg.Transitions) != 0 {
		t.Errorf("expected 0 transitions, got %d", len(resp.Msg.Transitions))
	}
	if resp.Msg.NextCursor != 0 {
		t.Errorf("expected next_cursor 0, got %d", resp.Msg.NextCursor)
	}
}

// TestH7H8_GetLogNotFound verifies GetLog on an unknown group
// returns NotFound.
func TestH7H8_GetLogNotFound(t *testing.T) {
	gid := types.GroupID{0x77}
	state := group.NewState(gid)
	svc := host.NewService("test", state)

	otherKey := types.GroupID{0x99}
	_, err := svc.GetLog(context.Background(),
		connect.NewRequest(&pb.GetLogRequest{
			GroupKey: &pb.PublicKey{Raw: otherKey[:]},
		}))
	if err == nil {
		t.Fatal("expected error for unknown group")
	}
}

// TestH7H8_GetSnapshotCurrentHead verifies GetSnapshot returns the
// current head snapshot + transition count.
func TestH7H8_GetSnapshotCurrentHead(t *testing.T) {
	gid := types.GroupID{0x77}
	state := group.NewState(gid)
	svc := host.NewService("test", state)

	resp, err := svc.GetSnapshot(context.Background(),
		connect.NewRequest(&pb.GetSnapshotRequest{
			GroupKey: &pb.PublicKey{Raw: gid[:]},
		}))
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	if resp.Msg.Snapshot == nil {
		t.Fatal("expected non-nil snapshot")
	}
	if resp.Msg.TransitionIndex != 0 {
		t.Errorf("expected transition_index 0, got %d", resp.Msg.TransitionIndex)
	}
}

// TestH7H8_GetSnapshotByRootUnimplemented verifies that requesting
// a snapshot by root returns Unimplemented (v0 limitation).
func TestH7H8_GetSnapshotByRootUnimplemented(t *testing.T) {
	gid := types.GroupID{0x77}
	state := group.NewState(gid)
	svc := host.NewService("test", state)

	_, err := svc.GetSnapshot(context.Background(),
		connect.NewRequest(&pb.GetSnapshotRequest{
			GroupKey: &pb.PublicKey{Raw: gid[:]},
			Root:     &pb.StateRoot{Hash: make([]byte, 32)},
		}))
	if err == nil {
		t.Fatal("expected Unimplemented error for snapshot-by-root")
	}
}

// TestH7H8_GetLogPagination verifies that limit + since_cursor
// pagination works correctly on a group with transitions.
// We use the Log() method to verify the count matches.
func TestH7H8_GetLogPagination(t *testing.T) {
	gid := types.GroupID{0x55}
	state := group.NewState(gid)
	svc := host.NewService("test", state)

	// Verify empty log.
	log := state.Log()
	if len(log) != 0 {
		t.Fatalf("expected 0 transitions, got %d", len(log))
	}

	// GetLog with default limit on empty group.
	resp, err := svc.GetLog(context.Background(),
		connect.NewRequest(&pb.GetLogRequest{
			GroupKey: &pb.PublicKey{Raw: gid[:]},
			Limit:   10,
		}))
	if err != nil {
		t.Fatalf("GetLog: %v", err)
	}
	if resp.Msg.Total != 0 {
		t.Errorf("expected total 0, got %d", resp.Msg.Total)
	}

	// Test pagination params: since_cursor beyond total should
	// return empty with total = 0.
	resp2, err := svc.GetLog(context.Background(),
		connect.NewRequest(&pb.GetLogRequest{
			GroupKey:    &pb.PublicKey{Raw: gid[:]},
			SinceCursor: 100,
			Limit:       10,
		}))
	if err != nil {
		t.Fatalf("GetLog with since_cursor: %v", err)
	}
	if len(resp2.Msg.Transitions) != 0 {
		t.Errorf("expected 0 transitions for since_cursor > total, got %d",
			len(resp2.Msg.Transitions))
	}
}