// SPDX-License-Identifier: AGPL-3.0
//
// Per-steward transition rate limiter (token bucket).
//
// Scott's threat model (#7): "a malicious steward floods
// SubmitTransition calls. Each transition is signed + broadcast.
// Mesh bandwidth consumed. Honest hosts waste CPU verifying
// signatures."
//
// Design: token bucket per (steward_pubkey, group_id). Each transition
// consumes one token. The bucket refills at a constant rate up to a
// burst cap. Tokens are restored lazily on check, not via a goroutine —
// pure function over world.Now().
//
// Determinism: the limiter takes a time source (a func() time.Time),
// not a wall clock. The simulator's virtual clock can drive it. In
// production the host's wall clock drives it. Same algorithm, same
// test surface.

package ratelimit

import (
	"fmt"
	"sync"
	"time"

	"github.com/sscoble/federated-meetup/internal/types"
)

// Bucket is a single token bucket. Tokens refill at Rate per second
// up to a max of Burst. Each Allow() consumes one token if available.
type Bucket struct {
	mu sync.Mutex

	Rate   float64       // tokens per second
	Burst  float64       // max tokens
	tokens float64       // current token count
	last   time.Time     // last refill timestamp
	clock  func() time.Time
}

// NewBucket constructs a bucket with the given rate (tokens/second)
// and burst (max tokens). Clock is the time source; pass nil to use
// time.Now (production).
func NewBucket(rate float64, burst float64, clock func() time.Time) *Bucket {
	if clock == nil {
		clock = time.Now
	}
	return &Bucket{
		Rate:   rate,
		Burst:  burst,
		tokens: burst,
		last:   clock(),
		clock:  clock,
	}
}

// Allow attempts to consume one token. Returns true if the call is
// permitted, false if the bucket is empty. If permitted, refills the
// bucket first based on elapsed time, then decrements.
//
// Refill is lazy: we never tick on a wall clock; we compute
// elapsed = now - last and add elapsed * rate tokens, capped at Burst.
func (b *Bucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.Rate
		if b.tokens > b.Burst {
			b.tokens = b.Burst
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Tokens returns the current token count (for diagnostics / tests).
// Calls Allow() implicitly to apply any pending refill.
func (b *Bucket) Tokens() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * b.Rate
		if b.tokens > b.Burst {
			b.tokens = b.Burst
		}
		b.last = now
	}
	return b.tokens
}

// Limiter is a collection of buckets keyed by (steward, group). It
// composes per-steward buckets so each steward has their own quota
// within each group.
//
// The Limiter is meant to live on a host (one per host). It uses the
// host's clock. Buckets are lazily created.
type Limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	clock   func() time.Time
	buckets map[bucketKey]*Bucket
}

// bucketKey is (group, steward). Composite key avoids allocating
// strings just to look up buckets.
type bucketKey struct {
	group   types.GroupID
	steward types.PublicKey
}

// NewLimiter constructs a limiter with the given rate (tokens/second)
// and burst (max tokens) applied per (steward, group) bucket.
// clock may be nil to use time.Now.
func NewLimiter(rate float64, burst float64, clock func() time.Time) *Limiter {
	if clock == nil {
		clock = time.Now
	}
	return &Limiter{
		rate:    rate,
		burst:   burst,
		clock:   clock,
		buckets: make(map[bucketKey]*Bucket),
	}
}

// Allow checks whether the given steward is allowed to submit a
// transition for the given group. Returns nil if permitted, or an
// ErrRateLimited describing the wait time if not.
//
// M-2: the entire lazy-create-and-allow sequence is under l.mu so
// that two concurrent calls for the same (group, steward) key cannot
// each create their own bucket and bypass the rate limit.
func (l *Limiter) Allow(group types.GroupID, steward types.PublicKey) error {
	k := bucketKey{group: group, steward: steward}
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[k]
	if !ok {
		b = NewBucket(l.rate, l.burst, l.clock)
		l.buckets[k] = b
	}
	if b.Allow() {
		return nil
	}
	return &ErrRateLimited{
		Steward:  steward,
		Group:    group,
		RetryIn:  retryAfter(b),
		Rate:     l.rate,
		Burst:    l.burst,
	}
}

// retryAfter estimates how long until the bucket has 1 token.
// Currently 1 / rate seconds for a token-bucket with no capacity
// headroom. A more accurate estimate would account for partial
// tokens already accumulated.
func retryAfter(b *Bucket) time.Duration {
	if b.Rate <= 0 {
		return time.Hour // pathological
	}
	// Tokens needed = 1 - b.Tokens() (always in [0, 1]).
	needed := 1.0 - b.Tokens()
	if needed <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) * needed / b.Rate)
}

// BucketCount returns the number of distinct (steward, group) buckets.
// Used by tests to verify lazy creation.
func (l *Limiter) BucketCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// ErrRateLimited is returned when a steward exceeds their quota.
type ErrRateLimited struct {
	Steward types.PublicKey
	Group   types.GroupID
	RetryIn time.Duration
	Rate    float64
	Burst   float64
}

func (e *ErrRateLimited) Error() string {
	return fmt.Sprintf("ratelimit: steward %x exceeded %v/s (burst %v) for group %x; retry in %v",
		e.Steward[:], e.Rate, e.Burst, e.Group[:], e.RetryIn)
}