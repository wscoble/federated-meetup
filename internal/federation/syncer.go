// SPDX-License-Identifier: AGPL-3.0
//
// Package federation implements the server-to-server sync that makes
// the protocol actually federated.
//
// The wire surface (GetLog, GetSnapshot, Subscribe, SubmitEvidence) is
// defined in the ConnectRPC proto and implemented by internal/host.
// This package is the other side: the CLIENT that calls those RPCs
// against a peer host and replays the transitions into a local
// *group.State.
//
// Two modes:
//
//   1. Bootstrap — pull the peer's entire transition log via GetLog
//      (paginated) and Apply each transition into the local state.
//      This is the "cold mirror" path. It's used when a host first
//      discovers a group it wants to mirror.
//
//   2. Live — after bootstrap, open a Subscribe stream to receive
//      new transitions in real-time and Apply them as they arrive.
//      This is the "hot mirror" path.
//
// The Syncer is designed to be embedded in the host daemon (cmd/fedmeetup)
// and configured with one or more peer URLs. It can also run standalone
// for testing.
//
// Design notes:
//   - The Syncer does NOT trust the peer. Every transition is verified
//     by Apply (signature checks, state-machine validation). A malicious
//     peer can only omit transitions, not inject fake ones.
//   - The Syncer is idempotent — if the local state already has a
//     transition at the same index, Apply will reject it as a duplicate
//     (the state machine checks the prior-state hash). This means
//     re-running bootstrap on an already-synced state is safe.
//   - The Syncer handles pagination internally. Callers just provide
//     the peer URL + group key and get a synced state.
//   - Live streaming auto-reconnects with a since_index set to the
//     local transition count, so missed events during a disconnect
//     are recovered on reconnect.

package federation

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"connectrpc.com/connect"

	pb "github.com/wscoble/federated-meetup/proto/federated_meetup/v1"
	"github.com/wscoble/federated-meetup/proto/federated_meetup/v1/federatedmeetupv1connect"

	"github.com/wscoble/federated-meetup/internal/group"
	"github.com/wscoble/federated-meetup/internal/types"
)

// Default page size for GetLog pagination.
const defaultPageSize = 100

// Default reconnect delay for live streaming.
const defaultReconnectDelay = 5 * time.Second

// PeerConfig describes a single peer host to sync from.
type PeerConfig struct {
	// BaseURL is the peer's HTTP base URL (e.g. "http://peer.example.com:8080").
	BaseURL string

	// GroupKey is the group to sync.
	GroupKey types.PublicKey
}

// Syncer pulls state from a peer host and maintains a local mirror.
//
// A Syncer is bound to a single local *group.State and a single peer.
// For multiple peers, create multiple Syncers (one per peer per group).
type Syncer struct {
	client federatedmeetupv1connect.HostServiceClient
	state  *group.State
	gk     types.PublicKey
	now    func() time.Time

	// reconnectDelay controls the delay between live-stream reconnect
	// attempts. Defaults to 5s.
	reconnectDelay time.Duration
}

// NewSyncer creates a Syncer that syncs from the given peer URL into
// the given local state.
//
// The httpClient is used for the ConnectRPC client. Pass http.DefaultClient
// for plaintext HTTP (v0 / local dev) or a TLS-configured client for
// production.
func NewSyncer(peerURL string, gk types.PublicKey, state *group.State, httpClient connect.HTTPClient) *Syncer {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Syncer{
		client: federatedmeetupv1connect.NewHostServiceClient(
			httpClient, peerURL,
		),
		state:          state,
		gk:             gk,
		now:            time.Now,
		reconnectDelay: defaultReconnectDelay,
	}
}

// SetClock overrides the time source (for tests).
func (s *Syncer) SetClock(now func() time.Time) { s.now = now }

// SetReconnectDelay overrides the live-stream reconnect delay.
func (s *Syncer) SetReconnectDelay(d time.Duration) { s.reconnectDelay = d }

// Bootstrap pulls the peer's entire transition log via GetLog and
// replays it into the local state. This is the cold-mirror path.
//
// It paginates through the log (page size = 100, server may cap at 1000)
// until the peer returns next_cursor = 0 (meaning no more data).
//
// Returns the total number of transitions applied. If the local state
// already has some transitions, it resumes from the local transition
// count (since_index = local count) to avoid re-applying.
//
// Idempotent: re-running Bootstrap on an already-synced state is safe
// (duplicates are rejected by Apply's prior-state hash check).
func (s *Syncer) Bootstrap(ctx context.Context) (uint64, error) {
	// Resume from the local state's current transition count.
	cursor := s.state.TransitionCount()
	var applied uint64

	for {
		if ctx.Err() != nil {
			return applied, ctx.Err()
		}

		resp, err := s.client.GetLog(ctx, connect.NewRequest(&pb.GetLogRequest{
			GroupKey:    &pb.PublicKey{Raw: s.gk[:]},
			SinceCursor: cursor,
			Limit:       defaultPageSize,
		}))
		if err != nil {
			return applied, fmt.Errorf("getlog at cursor %d: %w", cursor, err)
		}

		transitions := resp.Msg.GetTransitions()
		if len(transitions) == 0 {
			break
		}

		for _, pbT := range transitions {
			t, err := group.NewTransition(pbT, s.gk)
			if err != nil {
				return applied, fmt.Errorf("decode transition at index %d: %w", cursor, err)
			}
			if err := s.state.Apply(t, s.now()); err != nil {
				// If the transition is a duplicate (already applied),
				// skip it. This handles the case where we resumed
				// from a cursor that's slightly behind.
				if isDuplicateTransition(err) {
					continue
				}
				return applied, fmt.Errorf("apply transition at index %d: %w", cursor, err)
			}
			applied++
			cursor++
		}

		next := resp.Msg.GetNextCursor()
		if next == 0 {
			break
		}
		cursor = next
	}

	log.Printf("federation: bootstrap complete, applied %d transitions, local count=%d",
		applied, s.state.TransitionCount())
	return applied, nil
}

// Verify checks that the local state root matches the peer's snapshot
// root. This is the post-bootstrap integrity check.
//
// If the roots differ, the local state has diverged from the peer —
// either due to a bug, a fork, or a malicious peer. The caller should
// decide how to handle divergence (alert, re-bootstrap from scratch,
// or accept as a legitimate fork).
func (s *Syncer) Verify(ctx context.Context) error {
	resp, err := s.client.GetSnapshot(ctx, connect.NewRequest(&pb.GetSnapshotRequest{
		GroupKey: &pb.PublicKey{Raw: s.gk[:]},
	}))
	if err != nil {
		return fmt.Errorf("getsnapshot: %w", err)
	}

	peerRoot := resp.Msg.GetSnapshot().GetRoot()
	localRoot := s.state.Root()

	var peerHash types.Hash
	copy(peerHash[:], peerRoot.GetHash())

	if peerRoot == nil {
		return fmt.Errorf("peer snapshot has nil root")
	}

	if localRoot != peerHash {
		return fmt.Errorf("root mismatch: local=%x peer=%x (transition count: local=%d peer=%d)",
			localRoot[:], peerHash[:],
			s.state.TransitionCount(), resp.Msg.GetTransitionIndex())
	}
	return nil
}

// Live opens a Subscribe stream and applies incoming transitions in
// real-time. This is the hot-mirror path.
//
// It blocks until the context is cancelled. On stream errors, it
// reconnects after a delay, resuming from the local transition count
// (since_index) to recover any missed events.
//
// Call this AFTER Bootstrap to get a fully synced mirror.
func (s *Syncer) Live(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if err := s.liveOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			log.Printf("federation: live stream error: %v (reconnecting in %s)", err, s.reconnectDelay)
			select {
			case <-time.After(s.reconnectDelay):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// liveOnce opens one Subscribe stream and forwards events until it
// closes or errors.
func (s *Syncer) liveOnce(ctx context.Context) error {
	sinceIndex := s.state.TransitionCount()

	stream, err := s.client.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		GroupKey:   &pb.PublicKey{Raw: s.gk[:]},
		SinceIndex: sinceIndex,
	}))
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !stream.Receive() {
			if err := stream.Err(); err != nil {
				return fmt.Errorf("receive: %w", err)
			}
			return nil // clean EOF
		}

		ev := stream.Msg()

		pbT := ev.GetTransition()
		if pbT == nil {
			continue
		}

		t, err := group.NewTransition(pbT, s.gk)
		if err != nil {
			log.Printf("federation: decode error for live transition: %v", err)
			continue
		}

		if err := s.state.Apply(t, s.now()); err != nil {
			if isDuplicateTransition(err) {
				continue
			}
			log.Printf("federation: apply error for live transition: %v", err)
			continue
		}
	}
}

// isDuplicateTransition returns true if the error indicates a duplicate
// transition (one whose prior-state hash doesn't match because it's
// already been applied).
func isDuplicateTransition(err error) bool {
	if err == nil {
		return false
	}
	// The group state machine returns a *groupError for prior-state
	// mismatches. We check by string to avoid exporting error types.
	msg := err.Error()
	return contains(msg, "prior state mismatch") ||
		contains(msg, "expected prior state") ||
		contains(msg, "duplicate")
}

// contains is a case-insensitive substring check.
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || containsFold(s, sub))
}

func containsFold(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		match := true
		for j := 0; j < len(sub); j++ {
			a, b := s[i+j], sub[j]
			if a >= 'A' && a <= 'Z' {
				a += 32
			}
			if b >= 'A' && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}