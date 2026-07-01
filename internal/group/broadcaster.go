// SPDX-License-Identifier: MIT
//
// Broadcaster — fan-out for the Subscribe RPC (audit C-5, cycle 51).
//
// Each *group.State holds a Broadcaster. When Apply succeeds, the
// transition + new root + index is sent to every active subscriber.
// The Subscribe handler (host/service.go) registers a channel, the
// broadcaster fans out, and the handler forwards events to the
// ConnectRPC server stream.
//
// Design:
//   - Per-subscriber buffered channel (capacity 64). If a subscriber
//     is slow, the broadcaster drops the oldest event and logs a
//     warning — we never block Apply on a slow consumer.
//   - Subscribers are removed when their context is cancelled or
//     they call Unsubscribe.
//   - Thread-safe; the broadcaster is a standalone struct so it can
//     be tested independently.

package group

import (
	"log"
	"sync"
	"sync/atomic"
)

// TransitionEvent is the payload delivered to subscribers. It mirrors
// the proto TransitionEvent but is a plain Go struct so it doesn't
// import protobuf here.
type TransitionEvent struct {
	Transition *Transition
	NewRoot    [32]byte
	Index      uint64
}

// Broadcaster fans out TransitionEvents to a set of subscribers.
// Each subscriber is a buffered channel. If a subscriber's channel
// is full, the oldest event is dropped (non-blocking send).
type Broadcaster struct {
	mu      sync.Mutex
	subs    map[uint64]chan TransitionEvent
	nextID  uint64
	dropped atomic.Uint64
}

// NewBroadcaster creates an empty Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subs: make(map[uint64]chan TransitionEvent),
	}
}

// Subscribe registers a new subscriber and returns a receive-only
// channel + an unsubscribe function. The caller MUST call unsubscribe
// when done to avoid leaking the channel.
//
// The channel has a buffer of 64. If the subscriber reads slower
// than the producer, events are dropped (oldest first) and the
// dropped counter is incremented.
func (b *Broadcaster) Subscribe() (<-chan TransitionEvent, func()) {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	ch := make(chan TransitionEvent, 64)
	b.subs[id] = ch
	b.mu.Unlock()

	unsub := func() {
		b.mu.Lock()
		if ch, ok := b.subs[id]; ok {
			delete(b.subs, id)
			close(ch)
		}
		b.mu.Unlock()
	}

	return ch, unsub
}

// Broadcast sends an event to all subscribers. Non-blocking: if a
// subscriber's buffer is full, the oldest event is dropped.
func (b *Broadcaster) Broadcast(ev TransitionEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, ch := range b.subs {
		select {
		case ch <- ev:
		default:
			// Buffer full — drop oldest, push newest.
			select {
			case <-ch:
			default:
			}
			b.dropped.Add(1)
			log.Printf("WARN: broadcaster dropping event for sub %d (subscriber too slow), total_dropped=%d",
				id, b.dropped.Load())
			select {
			case ch <- ev:
			default:
			}
		}
	}
}

// Dropped returns the total number of events dropped due to slow
// subscribers. For observability + testing.
func (b *Broadcaster) Dropped() uint64 {
	return b.dropped.Load()
}

// SubscriberCount returns the current number of active subscribers.
func (b *Broadcaster) SubscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}