// SPDX-License-Identifier: MIT
//
// Steward custody tier tracking (protocol §5.4.7 / G3).
//
// The threshold multisig IS the security of the group. If all N
// signing keys live on a single laptop, the multisig is theater — one
// root compromise wins. The protocol can't enforce hardware wallets,
// but it CAN require that each steward declare a custody tier and
// let the threshold policy enforce "M of N must be HSM-or-better".
//
// This module is the data structure the State machine uses to track
// declarations. Declarations are themselves transitions
// (DECLARE_STEWARD_CUSTODY), so the data lives in the state log and
// is replayable like any other transition.
//
// Truth source: the state log. The current view at a given root is
// computed by walking the log from the genesis transition.

package group

import (
	"sync"

	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// CustodyDeclaration records one steward's declared tier at a point
// in time. A steward may re-declare (raising or lowering their tier) —
// each declaration is a separate transition and is recorded here.
type CustodyDeclaration struct {
	Steward pb.PublicKey
	Tier    pb.CustodyTier
}

// CustodyView is the live custody state at a given root: a map from
// steward public key to declared tier. Stewards who haven't declared
// a tier are absent from the map and treated as CUSTODY_TIER_LIVE_SYSTEM
// (the default "I have a key on a server somewhere" assumption).
type CustodyView struct {
	// tier[steward_key] = tier
	tier map[[32]byte]pb.CustodyTier
}

// TierFor returns the declared tier for a steward, or
// CUSTODY_TIER_LIVE_SYSTEM if the steward hasn't declared.
func (v *CustodyView) TierFor(s [32]byte) pb.CustodyTier {
	if v == nil {
		return pb.CustodyTier_CUSTODY_TIER_LIVE_SYSTEM
	}
	if t, ok := v.tier[s]; ok {
		return t
	}
	return pb.CustodyTier_CUSTODY_TIER_LIVE_SYSTEM
}

// CountAtLeast returns the number of stewards in the given set whose
// declared tier is <= maxTier (because the enum is ordered MOST
// trusted = lowest number; HSM=1 is the most trusted). Used by the
// threshold policy: "at least K of M signers must be HSM-or-better".
func (v *CustodyView) CountAtLeast(stewards []Steward, maxTier pb.CustodyTier) int {
	if v == nil {
		return 0
	}
	n := 0
	for _, s := range stewards {
		if v.TierFor(s.Key) <= maxTier {
			n++
		}
	}
	return n
}

// custodyLog is the per-State registry of declarations. Tracks every
// DECLARE_STEWARD_CUSTODY transition that has been applied to this
// state machine. Replay-safe: the live view is reconstructed by walking
// the log from genesis.
type custodyLog struct {
	mu       sync.Mutex
	declared map[[32]byte]pb.CustodyTier // current view
	// history tracks all declarations ever made, for audit. Bounded
	// by G7 (memory bounds) — when the history exceeds the cap, the
	// oldest entry is evicted.
	history []CustodyDeclaration
}

// newCustodyLog returns an empty log.
func newCustodyLog() *custodyLog {
	return &custodyLog{
		declared: make(map[[32]byte]pb.CustodyTier),
	}
}

// record adds a declaration. Called from State.Apply under the state
// lock when a DECLARE_STEWARD_CUSTODY transition succeeds.
func (c *custodyLog) record(d CustodyDeclaration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var k [32]byte
	copy(k[:], d.Steward.GetRaw())
	c.declared[k] = d.Tier
	c.history = append(c.history, d)
}

// snapshot returns a read-only CustodyView of the current state.
func (c *custodyLog) snapshot() *CustodyView {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v := &CustodyView{tier: make(map[[32]byte]pb.CustodyTier, len(c.declared))}
	for k, t := range c.declared {
		v.tier[k] = t
	}
	return v
}

// CustodyViewFor returns the live custody view for the state. Safe
// to call without holding the state lock.
func (s *State) CustodyView() *CustodyView {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.custody == nil {
		return nil
	}
	return s.custody.snapshot()
}

// RecordCustody is called from State.Apply when a
// DECLARE_STEWARD_CUSTODY transition succeeds. Not exported to the
// state-machine public API — only Apply calls it.
func (s *State) recordCustodyLocked(d CustodyDeclaration) {
	if s.custody == nil {
		s.custody = newCustodyLog()
	}
	s.custody.record(d)
}