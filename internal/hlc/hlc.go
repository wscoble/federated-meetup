// SPDX-License-Identifier: AGPL-3.0
//
// Package hlc implements Hybrid Logical Clocks (HLC) for the federation.
//
// An HLC is a single timestamp value (HLC) that gives:
//   - A total ordering across the federation, even when host wall-clocks
//     disagree.
//   - A wall-clock-shaped "approximately when" component, bounded by the
//     maximum drift between any two hosts in the federation.
//   - Detection of causality: if A observes B's message, A's next HLC will
//     be greater than B's.
//
// Format: a single 18-byte value (16 bytes nanos-since-epoch, 2 bytes
// counter). Comparable as a byte string. Stable across architectures.
//
// Design follows Kulkarni et al. (2014), "Logical Physical Clocks and
// their Applications in Distributed Systems", with simplifications:
//   - Counter is uint16, which is plenty for any single nanosecond.
//   - We rely on the caller's wall-clock source; this package is pure
//     (does not read clocks itself). The caller passes `now` to Now/Tick.
//   - Drift bound is the caller's concern: every host picks its own clock
//     source and drift assumption; we do not hard-code epsilon.
//
// Simulator integration: tests pass `world.Now()` as `now`. Hosts read
// their own HLC state on each transition they author, and merge remote
// HLCs from incoming transitions via Observe.

package hlc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

// HLC is a hybrid logical clock value. The zero value is the canonical
// "never set" sentinel — comparable as bytes, sorts before any real value.
type HLC []byte

// Size is the on-wire size of an HLC.
const Size = 18

// Zero is the canonical "never set" HLC. Comparable to all other HLCs
// (it sorts before any non-zero HLC).
var Zero = HLC(make([]byte, Size))

// New constructs an HLC from a wall-clock time. The counter starts at 0.
// The resulting HLC compares greater than Zero and less than any HLC
// derived from a later time.
func New(now time.Time) HLC {
	out := make([]byte, Size)
	binary.BigEndian.PutUint64(out[0:8], uint64(now.UnixNano()))
	binary.BigEndian.PutUint16(out[16:18], 0)
	return out
}

// FromProto deserializes an HLC from its protobuf bytes. Returns an error
// if the input is the wrong size.
func FromProto(b []byte) (HLC, error) {
	if len(b) == 0 {
		return Zero.Clone(), nil
	}
	if len(b) != Size {
		return nil, fmt.Errorf("hlc: bad size %d (want %d)", len(b), Size)
	}
	out := make([]byte, Size)
	copy(out, b)
	return out, nil
}

// Clone returns a deep copy of the HLC. HLC values are immutable; Clone
// makes that explicit.
func (h HLC) Clone() HLC {
	if len(h) == 0 {
		return Zero.Clone()
	}
	out := make([]byte, len(h))
	copy(out, h)
	return out
}

// Time returns the wall-clock component as a time.Time. Useful for
// audit logs and human-facing UIs. Not authoritative for ordering.
func (h HLC) Time() time.Time {
	if len(h) != Size {
		return time.Time{}
	}
	ns := binary.BigEndian.Uint64(h[0:8])
	return time.Unix(0, int64(ns)).UTC()
}

// Counter returns the logical counter component.
func (h HLC) Counter() uint16 {
	if len(h) != Size {
		return 0
	}
	return binary.BigEndian.Uint16(h[16:18])
}

// Compare returns -1 if h < other, 0 if h == other, +1 if h > other.
// HLCs are compared as their 18-byte big-endian representation. The wall
// component dominates the counter, so two HLCs with different wall
// components always order by wall component regardless of counter.
func (h HLC) Compare(other HLC) int {
	return bytes.Compare(h, other)
}

// After reports whether h > other.
func (h HLC) After(other HLC) bool { return h.Compare(other) > 0 }

// Before reports whether h < other.
func (h HLC) Before(other HLC) bool { return h.Compare(other) < 0 }

// Equal reports whether h == other.
func (h HLC) Equal(other HLC) bool { return h.Compare(other) == 0 }

// =============================================================================
// Tick / Observe — the two operations a host calls to maintain its clock.
// =============================================================================

// PartitionWindow is the wall-clock jump threshold (in nanoseconds)
// below which Tick preserves the counter on a wall advance. If the
// wall clock moves forward by less than this window and the prior
// counter was non-zero, Tick bumps the counter instead of resetting
// it to 0 — preserving monotonicity for events that were pending
// during a short partition. (Audit H-3.)
//
// Default: 60 seconds. A wall jump beyond this is treated as a true
// disconnection (host was offline long enough that counter continuity
// is irrelevant); the counter resets to 0.
const PartitionWindow = 60 * int64(time.Second)

// PartitionWindowNanos is PartitionWindow in nanoseconds, pre-computed
// for use in the hot path of Tick.
var PartitionWindowNanos = uint64(PartitionWindow)

// Tick returns the HLC a host should assign to a locally-generated event
// at wall-clock time `now`. The caller maintains its own prior HLC and
// passes it in. Tick is the monotonic-local-time primitive.
//
// Properties:
//   - h_out > h_in (strictly, when h_in != Zero)
//   - h_out.Time() >= now (or h_in.Time() if h_in is more recent)
//   - h_out is unique to this call (counter increments)
func Tick(last HLC, now time.Time) (HLC, error) {
	if last == nil {
		last = Zero
	}
	if len(last) != 0 && len(last) != Size {
		return nil, fmt.Errorf("hlc: bad prior size %d", len(last))
	}

	nowNanos := uint64(now.UnixNano())
	var lastNanos uint64
	if len(last) == Size {
		lastNanos = binary.BigEndian.Uint64(last[0:8])
	}

	out := make([]byte, Size)

	switch {
	case len(last) != Size:
		// First ever tick (last == Zero) or malformed input treated as
		// Zero. Counter starts at 0.
		binary.BigEndian.PutUint64(out[0:8], nowNanos)
		binary.BigEndian.PutUint16(out[16:18], 0)
	case lastNanos >= nowNanos:
		// Local clock didn't move forward (same nanosecond or went back
		// due to NTP step / suspend-resume). Stick with last's wall
		// component and bump the counter to preserve strict monotonicity.
		copy(out[0:8], last[0:8])
		counter := binary.BigEndian.Uint16(last[16:18])
		if counter == 0xFFFF {
			// Counter overflow. The HLC paper prescribes incrementing
			// the wall component by 1 nanosecond. This is exceedingly
			// rare — 65535 events in a single nanosecond.
			ns := binary.BigEndian.Uint64(last[0:8]) + 1
			binary.BigEndian.PutUint64(out[0:8], ns)
			binary.BigEndian.PutUint16(out[16:18], 0)
		} else {
			binary.BigEndian.PutUint16(out[16:18], counter+1)
		}
	default:
		// Local clock advanced strictly past last (the common case).
		//
		// H-3 partition-aware mode: if the wall jump is "recent" (within
		// PartitionWindow, default 60s) and the prior counter was non-zero,
		// preserve counter continuity by bumping counter+1 instead of
		// resetting to 0. This preserves monotonicity for events that were
		// pending during a short partition / wall-clock adjustment.
		//
		// Only reset to 0 if the wall has advanced by more than
		// PartitionWindow — a true disconnection where counter continuity
		// is irrelevant.
		counter := binary.BigEndian.Uint16(last[16:18])
		jump := nowNanos - lastNanos
		if counter > 0 && jump <= PartitionWindowNanos {
			// Recent wall advance with pending counter — bump counter
			// to preserve monotonicity across the partition.
			binary.BigEndian.PutUint64(out[0:8], nowNanos)
			if counter == 0xFFFF {
				// Overflow: bump wall by 1ns, reset counter.
				binary.BigEndian.PutUint64(out[0:8], nowNanos+1)
				binary.BigEndian.PutUint16(out[16:18], 0)
			} else {
				binary.BigEndian.PutUint16(out[16:18], counter+1)
			}
		} else {
			// Either the counter was already 0 (normal case) or the wall
			// jumped beyond the partition window (true disconnection).
			// Counter resets to 0; the wall component dominates ordering.
			binary.BigEndian.PutUint64(out[0:8], nowNanos)
			binary.BigEndian.PutUint16(out[16:18], 0)
		}
	}

	return out, nil
}

// Observe is called when a host receives a message with a remote HLC. The
// host's local HLC advances to ensure it never issues a value <= the
// remote's. This is the HLC property that makes causality total.
//
// Properties:
//   - h_out > h_remote
//   - h_out > h_local
//   - h_out.Time() >= max(now, h_remote.Time(), h_local.Time())
//
// Call this once per inbound message that carries an HLC, BEFORE Tick
// for any locally-generated event that depends on it.
func Observe(last, remote HLC, now time.Time) (HLC, error) {
	if last == nil {
		last = Zero
	}
	if remote == nil {
		remote = Zero
	}
	for _, h := range []HLC{last, remote} {
		if len(h) != 0 && len(h) != Size {
			return nil, fmt.Errorf("hlc: bad HLC size %d", len(h))
		}
	}

	nowNanos := uint64(now.UnixNano())
	var lastNanos, remoteNanos uint64
	if len(last) == Size {
		lastNanos = binary.BigEndian.Uint64(last[0:8])
	}
	if len(remote) == Size {
		remoteNanos = binary.BigEndian.Uint64(remote[0:8])
	}

	maxNanos := nowNanos
	if lastNanos > maxNanos {
		maxNanos = lastNanos
	}
	if remoteNanos > maxNanos {
		maxNanos = remoteNanos
	}

	out := make([]byte, Size)
	binary.BigEndian.PutUint64(out[0:8], maxNanos)

	switch {
	case maxNanos == remoteNanos && remoteNanos == lastNanos:
		// Both last and remote equal max. Counter is max+1 of both.
		lc := uint16(0)
		if len(last) == Size {
			lc = binary.BigEndian.Uint16(last[16:18])
		}
		rc := uint16(0)
		if len(remote) == Size {
			rc = binary.BigEndian.Uint16(remote[16:18])
		}
		counter := lc
		if rc > counter {
			counter = rc
		}
		if counter == 0xFFFF {
			// Overflow: bump wall by 1, reset counter.
			binary.BigEndian.PutUint64(out[0:8], maxNanos+1)
			binary.BigEndian.PutUint16(out[16:18], 0)
		} else {
			binary.BigEndian.PutUint16(out[16:18], counter+1)
		}
	case maxNanos == remoteNanos:
		// Remote is the dominant max. Counter = remote+1.
		rc := uint16(0)
		if len(remote) == Size {
			rc = binary.BigEndian.Uint16(remote[16:18])
		}
		if rc == 0xFFFF {
			binary.BigEndian.PutUint64(out[0:8], maxNanos+1)
			binary.BigEndian.PutUint16(out[16:18], 0)
		} else {
			binary.BigEndian.PutUint16(out[16:18], rc+1)
		}
	case maxNanos == lastNanos:
		// Local is dominant max. Counter = last+1.
		lc := uint16(0)
		if len(last) == Size {
			lc = binary.BigEndian.Uint16(last[16:18])
		}
		if lc == 0xFFFF {
			binary.BigEndian.PutUint64(out[0:8], maxNanos+1)
			binary.BigEndian.PutUint16(out[16:18], 0)
		} else {
			binary.BigEndian.PutUint16(out[16:18], lc+1)
		}
	default:
		// max is `now`. Counter resets to 0.
		binary.BigEndian.PutUint16(out[16:18], 0)
	}

	return out, nil
}

// =============================================================================
// Convenience constructors
// =============================================================================

// ErrInvalidHLC is returned by operations on malformed HLCs.
var ErrInvalidHLC = errors.New("hlc: invalid value")

// String returns a debug-friendly representation: "T.RFC3339Nano|counter".
func (h HLC) String() string {
	if len(h) != Size {
		return "<invalid-hlc>"
	}
	return fmt.Sprintf("%s|c%d", h.Time().Format(time.RFC3339Nano), h.Counter())
}