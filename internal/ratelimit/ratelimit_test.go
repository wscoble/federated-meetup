// SPDX-License-Identifier: AGPL-3.0
//
// Unit tests for the rate-limit primitive. Verifies token-bucket
// semantics in isolation before the integration test in sim/threat_test.go
// exercises it through group.State.Apply.

package ratelimit

import (
	"errors"
	"testing"
	"time"

	"github.com/wscoble/federated-meetup/internal/types"
)

// clockFn returns a deterministic clock anchored at t. The returned
// function returns t+offset whenever called. We capture the call count
// so tests can verify decay happens.
func clockFn(t time.Time) (func() time.Time, *int) {
	var calls int
	return func() time.Time {
		calls++
		return t
	}, &calls
}

func TestBucket_AllowsBurst(t *testing.T) {
	now := time.Unix(1700000000, 0)
	clock, _ := clockFn(now)
	b := NewBucket(10, 5, clock) // 10/s, burst 5

	// First 5 calls succeed (burst).
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Fatalf("Allow #%d should succeed (within burst)", i)
		}
	}
	// 6th call fails.
	if b.Allow() {
		t.Fatal("Allow #6 should fail (burst exhausted)")
	}
}

func TestBucket_RefillsOverTime(t *testing.T) {
	start := time.Unix(1700000000, 0)
	cur := start
	clock := func() time.Time { return cur }

	b := NewBucket(10, 5, clock) // 10/s, burst 5

	// Drain the bucket.
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Fatalf("drain #%d failed", i)
		}
	}
	if b.Allow() {
		t.Fatal("post-drain should fail")
	}

	// Advance 1 second — should refill 10 tokens, capped at burst (5).
	cur = start.Add(1 * time.Second)
	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Fatalf("after 1s refill, call #%d should succeed (5 burst)", i)
		}
	}
	// 6th call (burst re-exhausted, no further refill since clock
	// returns the same value across all these calls).
	if b.Allow() {
		t.Fatal("after 1s, 6th call should fail (burst re-exhausted)")
	}
}

func TestBucket_PartialRefill(t *testing.T) {
	start := time.Unix(1700000000, 0)
	cur := start
	clock := func() time.Time { return cur }

	b := NewBucket(1, 1, clock) // 1/s, burst 1

	if !b.Allow() {
		t.Fatal("first should succeed")
	}
	if b.Allow() {
		t.Fatal("immediate second should fail")
	}

	// Advance 0.5s — partial refill = 0.5 tokens, still under 1.
	cur = start.Add(500 * time.Millisecond)
	if b.Allow() {
		t.Fatal("at 0.5s, should still fail (only 0.5 tokens accumulated)")
	}

	// Advance another 0.6s — total 1.1s, accumulated 1.1 tokens.
	cur = start.Add(1100 * time.Millisecond)
	if !b.Allow() {
		t.Fatal("at 1.1s, should succeed")
	}
}

func TestLimiter_PerStewardQuota(t *testing.T) {
	now := time.Unix(1700000000, 0)
	clock, _ := clockFn(now)
	l := NewLimiter(10, 3, clock) // 10/s, burst 3, per (steward, group)

	groupID := types.GroupID{1, 2, 3}
	alice := types.PublicKey{1}
	bob := types.PublicKey{2}

	// Alice: 3 allowed, 4th rejected.
	for i := 0; i < 3; i++ {
		if err := l.Allow(groupID, alice); err != nil {
			t.Fatalf("alice #%d should succeed: %v", i, err)
		}
	}
	err := l.Allow(groupID, alice)
	if err == nil {
		t.Fatal("alice #4 should be rate-limited")
	}
	var rl *ErrRateLimited
	if !errors.As(err, &rl) {
		t.Fatalf("expected ErrRateLimited, got %T: %v", err, err)
	}
	if rl.Steward != alice {
		t.Fatal("error should reference alice")
	}
	if rl.RetryIn <= 0 {
		t.Fatal("retry duration should be positive")
	}

	// Bob has his own bucket — should still succeed.
	for i := 0; i < 3; i++ {
		if err := l.Allow(groupID, bob); err != nil {
			t.Fatalf("bob #%d should succeed (separate bucket): %v", i, err)
		}
	}
}

func TestLimiter_BucketCreationIsLazy(t *testing.T) {
	now := time.Unix(1700000000, 0)
	clock, _ := clockFn(now)
	l := NewLimiter(10, 1, clock)

	groupID := types.GroupID{1}
	steward := types.PublicKey{99}

	if got := l.BucketCount(); got != 0 {
		t.Fatalf("expected 0 buckets before any Allow, got %d", got)
	}
	_ = l.Allow(groupID, steward)
	if got := l.BucketCount(); got != 1 {
		t.Fatalf("expected 1 bucket after Allow, got %d", got)
	}
}

func TestLimiter_NilClockUsesTimeNow(t *testing.T) {
	l := NewLimiter(1000, 5, nil) // nil → time.Now
	groupID := types.GroupID{1}
	steward := types.PublicKey{1}
	// Just verify it doesn't panic and accepts requests.
	for i := 0; i < 5; i++ {
		if err := l.Allow(groupID, steward); err != nil {
			t.Fatalf("nil-clock Allow #%d: %v", i, err)
		}
	}
}