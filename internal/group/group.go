// SPDX-License-Identifier: MIT
//
// Package group is the in-memory group state machine. A group is identified
// by its keypair. Its state is a Merkle KV store. Transitions are signed by
// the steward set (multisig envelope, threshold >= N).
//
// Two roles:
//   - State: the host's view of one group. Apply transitions, get snapshots.
//   - Transition: a signed change to the state machine.
//
// The on-wire format (protobuf) is in proto/federated_meetup/v1/state.proto.
// These types are the canonical Go in-memory representations.

package group

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/sscoble/federated-meetup/internal/crypto"
	"github.com/sscoble/federated-meetup/internal/ratelimit"
	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// GroupID re-exported for clarity.
type GroupID = types.GroupID

// =============================================================================
// Transition
// =============================================================================

// Transition is the in-memory representation of a state change.
type Transition struct {
	// The protobuf message that was signed.
	Proto *pb.Transition

	// Cached canonical bytes for signature verification.
	canonical []byte

	// Cached group ID. Populated by NewTransitionWithGroup, since the
	// protobuf message alone does not carry the group ID (the on-wire
	// envelope carries it).
	groupID types.GroupID
}

// NewTransition constructs a transition from a protobuf message. The
// canonical bytes are computed eagerly so signing/verification is cheap.
// The group ID must be provided separately — the proto does not embed it.
func NewTransition(t *pb.Transition, gid types.GroupID) (*Transition, error) {
	cp := proto.Clone(t).(*pb.Transition)
	cp.StewardSignatures = nil
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(cp)
	if err != nil {
		return nil, fmt.Errorf("group: marshal: %w", err)
	}
	return &Transition{
		Proto:     t,
		canonical: canonical,
		groupID:   gid,
	}, nil
}

// GroupID returns the group this transition belongs to.
func (t *Transition) GroupID() GroupID { return t.groupID }

// Canonical returns the canonical sign-bytes for this transition.
func (t *Transition) Canonical() []byte { return t.canonical }

// MarshalCanonicalForSigning is a helper used by tests to compute the bytes
// that should be signed for a transition (without the transition itself
// having a Go value yet). Equivalent to NewTransition(...).Canonical().
func MarshalCanonicalForSigningHelper(t *pb.Transition) ([]byte, error) {
	cp := proto.Clone(t).(*pb.Transition)
	cp.StewardSignatures = nil
	return proto.MarshalOptions{Deterministic: true}.Marshal(cp)
}

// VerifyStewardSignatures checks the multisig envelope against the steward
// set of the group at the prior_state the transition references.
func (t *Transition) VerifyStewardSignatures(st *State) error {
	stewards, threshold := st.StewardsAndThresholdAt(t.Proto.GetPriorState())
	return t.verifyStewardSignaturesWith(stewards, threshold)
}

// VerifyStewardSignaturesLocked is the same as VerifyStewardSignatures
// but assumes the caller already holds st.mu. Use this from inside State
// methods that already hold the lock to avoid recursive-locking deadlocks.
func (t *Transition) VerifyStewardSignaturesLocked(st *State) error {
	stewards, threshold := st.stewardsAndThresholdAtLocked(t.Proto.GetPriorState())
	return t.verifyStewardSignaturesWith(stewards, threshold)
}

func (t *Transition) verifyStewardSignaturesWith(stewards []Steward, threshold uint32) error {
	multisig := t.Proto.GetStewardSignatures()
	if multisig == nil {
		return errors.New("group: transition has no steward signatures")
	}
	sigs := make([]types.Signature, 0, len(multisig.GetSignatures()))
	for _, s := range multisig.GetSignatures() {
		var sig types.Signature
		copy(sig[:], s.GetRaw())
		sigs = append(sigs, sig)
	}
	stewardKeys := make([]types.PublicKey, len(stewards))
	for i, s := range stewards {
		stewardKeys[i] = s.Key
	}
	return crypto.VerifyMultisig(stewardKeys, threshold, sigs, t.groupID, crypto.MsgKindTransition, t.canonical)
}

// =============================================================================
// State
// =============================================================================

// State is the host's view of one group's state machine.
type State struct {
	groupID GroupID

	mu sync.Mutex

	// Current canonical snapshot.
	snapshot types.StateSnapshot

	// History of stewards as of each prior_state hash. The current stewards
	// are at the head. Indexing by state root lets us resolve stewards at
	// any past snapshot (e.g. when verifying a transition whose prior_state
	// is older).
	stewardHistory map[types.Hash][]Steward

	// History of thresholds. Same indexing scheme.
	thresholdHistory map[types.Hash]uint32

	// Per-key sequence numbers. Used to detect out-of-order writes within a
	// key (the spec uses these for ordering, not for crypto).
	keySeq map[string]uint64

	// The initial threshold and stewards. The current view extends from
	// here.
	initialStewards []Steward
	initialThreshold uint32

	// Transition log, for replay / verification.
	log []*Transition

	// equivocation tracks every (steward, prior_state) we've applied,
	// so a second distinct signed transition at the same point is
	// detected as insider equivocation. See equivocation.go.
	equivocation *equivocationLog

	// MaxStewards caps the size of the steward set. Apply rejects
	// ADD_STEWARD once the current set reaches this size. Zero means
	// no cap (legacy / test mode). Default in NewState is 100.
	MaxStewards int

	// Limiter rate-limits transitions per (steward, group). When nil
	// (the default), no rate limit is enforced. Hosts opt in via
	// SetLimiter to defend against transition flooding (§5.4.5).
	//
	// The limiter is invoked under s.mu so its own internal locking
	// is only protecting against the limiter's lazy bucket creation,
	// not against concurrent Apply calls.
	Limiter *ratelimit.Limiter
}

// Steward is a public key + role attestation. v1 has no roles; the steward
// set is flat.
type Steward struct {
	Key types.PublicKey
	// Role would go here. Reserved.
}

// NewState creates an empty state for a group with the given ID. The group
// has no transitions yet — Apply() with a CREATE_GROUP transition to
// initialize.
//
// MaxStewards defaults to 100 (per protocol hardening). Callers can
// override by setting the field directly after construction.
func NewState(gid GroupID) *State {
	return &State{
		groupID:          gid,
		stewardHistory:   make(map[types.Hash][]Steward),
		thresholdHistory: make(map[types.Hash]uint32),
		keySeq:           make(map[string]uint64),
		MaxStewards:      100,
	}
}

// Snapshot returns the current state snapshot.
func (s *State) Snapshot() types.StateSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot
}

// Root returns the current state root.
func (s *State) Root() types.Hash {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshot.Root()
}

// Stewards returns the steward set at the current state head.
func (s *State) Stewards() []Steward {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stewardsAtLocked(nil)
}

// Threshold returns the threshold at the current state head.
func (s *State) Threshold() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.thresholdAtLocked(nil)
}

// StewardsAt returns the steward set as of the given state root. If the root
// is empty, returns the current steward set. If the root is unknown,
// returns an empty slice (which will cause signature verification to fail).
func (s *State) StewardsAt(root *pb.StateRoot) []Steward {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stewardsAtLocked(root)
}

// stewardsAtLocked is the same as StewardsAt but assumes the caller holds s.mu.
func (s *State) stewardsAtLocked(root *pb.StateRoot) []Steward {
	if root == nil || len(root.GetHash()) == 0 {
		// Use the current (head) steward set. The most recent history
		// entry is keyed by the current state root.
		if st, ok := s.stewardHistory[s.snapshot.Root()]; ok {
			return append([]Steward(nil), st...)
		}
		return append([]Steward(nil), s.initialStewards...)
	}
	var h types.Hash
	copy(h[:], root.GetHash())
	if st, ok := s.stewardHistory[h]; ok {
		return append([]Steward(nil), st...)
	}
	return nil
}

// ThresholdAt returns the threshold as of the given state root. Same lookup
// semantics as StewardsAt.
func (s *State) ThresholdAt(root *pb.StateRoot) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.thresholdAtLocked(root)
}

// StewardsAndThresholdAt returns both the steward set and threshold at
// the given state root, atomically (under a single lock acquisition).
// Prefer this over calling StewardsAt and ThresholdAt separately when
// both are needed — it avoids a race where the steward set changes
// between the two reads.
func (s *State) StewardsAndThresholdAt(root *pb.StateRoot) ([]Steward, uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stewardsAtLocked(root), s.thresholdAtLocked(root)
}

// stewardsAndThresholdAtLocked is the same as StewardsAndThresholdAt
// but assumes the caller holds s.mu.
func (s *State) stewardsAndThresholdAtLocked(root *pb.StateRoot) ([]Steward, uint32) {
	return s.stewardsAtLocked(root), s.thresholdAtLocked(root)
}

func (s *State) thresholdAtLocked(root *pb.StateRoot) uint32 {
	if root == nil || len(root.GetHash()) == 0 {
		if t, ok := s.thresholdHistory[s.snapshot.Root()]; ok {
			return t
		}
		return s.initialThreshold
	}
	var h types.Hash
	copy(h[:], root.GetHash())
	if t, ok := s.thresholdHistory[h]; ok {
		return t
	}
	return 0
}

// Apply validates the transition's signatures against the current steward
// set and prior_state, applies the changes, and advances the state machine.
func (s *State) Apply(t *Transition, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Verify the prior_state matches our current head.
	currentRoot := s.snapshot.Root()
	if t.Proto.GetPriorState() != nil && len(t.Proto.GetPriorState().GetHash()) > 0 {
		var prior types.Hash
		copy(prior[:], t.Proto.GetPriorState().GetHash())
		if prior != currentRoot {
			return fmt.Errorf("group: prior_state mismatch (have %x, got %x)", currentRoot, prior)
		}
	}

	// Equivocation check: if any verifying steward has already signed
	// a different transition at the same prior_state, refuse to apply
	// this one. The first transition to arrive is canonical; the
	// second is the equivocation evidence. Honest hosts that observe
	// both can publish the evidence and slash the offending key.
	if t.Proto.GetPriorState() != nil && len(t.Proto.GetPriorState().GetHash()) > 0 {
		var prior types.Hash
		copy(prior[:], t.Proto.GetPriorState().GetHash())
		// The signing steward is whichever pubkey in the multisig
		// verifies. We pick the first match; equivocation by ANY
		// steward is enough to reject.
		stewardsForCheck := s.stewardsAtLocked(t.Proto.GetPriorState())
		signing := t.findSigningSteward(stewardsForCheck)
		if signing != (types.PublicKey{}) {
			// Rate-limit check (§5.4.5). Charge the signing steward's
			// bucket for this group BEFORE the equivocation check, so
			// that rate-limited attempts don't pollute the equivocation
			// log with phantom entries.
			if s.Limiter != nil {
				if err := s.Limiter.Allow(s.groupID, signing); err != nil {
					return err
				}
			}
			txHash := transitionTxHash(t)
			isEquiv := s.checkEquivocationLocked(signing, prior, t.Proto.GetHlc(), txHash)
			if isEquiv {
				return fmt.Errorf("group: equivocation detected — steward %x signed a conflicting transition at prior_state %x", signing[:8], prior[:8])
			}
		}
	}

	// Verify signatures.
	stewards := s.stewardsAtLocked(t.Proto.GetPriorState())
	if len(stewards) == 0 {
		// First transition — must be CREATE_GROUP, and the signatures must
		// come from the initial steward set declared in the payload.
		if t.Proto.GetType() != pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP {
			return errors.New("group: first transition must be CREATE_GROUP")
		}
		p := t.Proto.GetCreateGroup()
		if p == nil {
			return errors.New("group: CREATE_GROUP missing payload")
		}
		// For CREATE_GROUP, signatures must come from at least `threshold`
		// of the initial_stewards.
		initStewards := make([]types.PublicKey, len(p.GetInitialStewards()))
		for i, k := range p.GetInitialStewards() {
			copy(initStewards[i][:], k.GetRaw())
		}
		multisig := t.Proto.GetStewardSignatures()
		if multisig == nil || uint32(len(multisig.GetSignatures())) < p.GetThreshold() {
			return errors.New("group: CREATE_GROUP has insufficient signatures for declared threshold")
		}
		sigs := make([]types.Signature, 0, len(multisig.GetSignatures()))
		for _, sg := range multisig.GetSignatures() {
			var sig types.Signature
			copy(sig[:], sg.GetRaw())
			sigs = append(sigs, sig)
		}
		if err := crypto.VerifyMultisig(initStewards, p.GetThreshold(), sigs, t.groupID, crypto.MsgKindTransition, t.canonical); err != nil {
			return fmt.Errorf("group: CREATE_GROUP signature verification: %w", err)
		}
		// Initialize the steward set and threshold.
		s.initialStewards = make([]Steward, len(initStewards))
		for i, k := range initStewards {
			s.initialStewards[i] = Steward{Key: k}
		}
		s.initialThreshold = p.GetThreshold()
	} else {
		// Non-initial transitions. Use the locked variant — Apply
		// already holds s.mu; calling the public VerifyStewardSignatures
		// would deadlock on the recursive mutex.
		if err := t.VerifyStewardSignaturesLocked(s); err != nil {
			return fmt.Errorf("group: signature verification: %w", err)
		}
	}

	// Apply the payload to the state.
	newEntries := append([]types.StateEntry(nil), s.snapshot.Entries...)
	switch t.Proto.GetType() {
	case pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP:
		p := t.Proto.GetCreateGroup()
		newEntries = appendOrUpdate(newEntries, "name", []byte(p.GetCanonicalName()))
		newEntries = appendOrUpdate(newEntries, "display_name", []byte(p.GetDisplayName()))
		// Steward history at the NEW root records the initial set.
	case pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD:
		p := t.Proto.GetAddSteward()
		var key types.PublicKey
		copy(key[:], p.GetNewSteward().GetRaw())
		// Cap the steward set: refuse to grow past MaxStewards.
		// Compute the PROSPECTIVE steward set (current stewards + new
		// key, deduped) and check against the cap. This ensures the
		// check uses the post-transition count, not the stale
		// pre-transition count from the prior root's history.
		prospective := prospectiveStewardsAfterAddLocked(s, key)
		if s.MaxStewards > 0 && len(prospective) > s.MaxStewards {
			return fmt.Errorf("group: ADD_STEWARD rejected — steward set would grow to %d, MaxStewards=%d", len(prospective), s.MaxStewards)
		}
		newEntries = appendOrUpdate(newEntries, fmt.Sprintf("steward/%x", key[:]), []byte{1})
	case pb.TransitionType_TRANSITION_TYPE_REMOVE_STEWARD:
		p := t.Proto.GetRemoveSteward()
		var key types.PublicKey
		copy(key[:], p.GetSteward().GetRaw())
		newEntries = appendOrUpdate(newEntries, fmt.Sprintf("steward/%x", key[:]), nil)
	case pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD:
		p := t.Proto.GetChangeThreshold()
		newEntries = appendOrUpdate(newEntries, "threshold", binaryUint32(p.GetNewThreshold()))
	case pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT:
		p := t.Proto.GetCreateEvent()
		// Store the event payload as protobuf bytes keyed by event_id.
		ep, err := proto.Marshal(p)
		if err != nil {
			return fmt.Errorf("group: marshal event: %w", err)
		}
		newEntries = appendOrUpdate(newEntries, "event/"+p.GetEventId(), ep)
	case pb.TransitionType_TRANSITION_TYPE_CANCEL_EVENT:
		p := t.Proto.GetCancelEvent()
		newEntries = appendOrUpdate(newEntries, "event_cancelled/"+p.GetEventId(), []byte{1})
	case pb.TransitionType_TRANSITION_TYPE_RSVP:
		p := t.Proto.GetRsvp()
		var user types.PublicKey
		copy(user[:], p.GetUser().GetRaw())
		newEntries = appendOrUpdate(newEntries, fmt.Sprintf("rsvp/%s/%x", p.GetEventId(), user[:]), []byte{1})
	case pb.TransitionType_TRANSITION_TYPE_CANCEL_RSVP:
		p := t.Proto.GetCancelRsvp()
		var user types.PublicKey
		copy(user[:], p.GetUser().GetRaw())
		newEntries = appendOrUpdate(newEntries, fmt.Sprintf("rsvp/%s/%x", p.GetEventId(), user[:]), nil)
	case pb.TransitionType_TRANSITION_TYPE_ATTEST:
		p := t.Proto.GetAttest()
		attestKey := attestStorageKey(p)
		attestBytes, err := proto.Marshal(p)
		if err != nil {
			return fmt.Errorf("group: marshal attest: %w", err)
		}
		newEntries = appendOrUpdate(newEntries, attestKey, attestBytes)
	case pb.TransitionType_TRANSITION_TYPE_FORK:
		// Fork creates a NEW group; the parent group's state machine just
		// records the fork line. The new group is built separately.
		p := t.Proto.GetFork()
		newEntries = appendOrUpdate(newEntries, "fork_lineage", []byte(p.GetNewGroupKey().GetRaw()))
	case pb.TransitionType_TRANSITION_TYPE_MIGRATE:
		p := t.Proto.GetMigrate()
		newEntries = appendOrUpdate(newEntries, "canonical_host", []byte(p.GetNewHost()))
		newEntries = appendOrUpdate(newEntries, "canonical_after", binaryUint64(uint64(p.GetDeadline().GetSeconds())))
	default:
		return fmt.Errorf("group: unsupported transition type %v", t.Proto.GetType())
	}

	s.snapshot = types.StateSnapshot{Entries: newEntries}
	s.stewardHistory[s.snapshot.Root()] = s.computeCurrentStewards(t.Proto)
	s.thresholdHistory[s.snapshot.Root()] = s.computeCurrentThreshold(t.Proto)
	s.log = append(s.log, t)
	return nil
}

// computeCurrentStewards returns the steward set after applying t.
// Always walks back via prior_state — this is the only way to get the
// pre-transition steward set right when the new root has no history
// entry yet (which is the common case during Apply, since the entry
// is written AFTER this function returns).
func (s *State) computeCurrentStewards(t *pb.Transition) []Steward {
	current := s.stewardsAtLocked(t.GetPriorState())
	switch t.GetType() {
	case pb.TransitionType_TRANSITION_TYPE_ADD_STEWARD:
		var key types.PublicKey
		copy(key[:], t.GetAddSteward().GetNewSteward().GetRaw())
		// Dedupe: if the key is already in the set, the transition
		// is a no-op for steward set growth but the multisig still
		// needs to verify.
		for _, st := range current {
			if st.Key == key {
				return current
			}
		}
		current = append(current, Steward{Key: key})
	case pb.TransitionType_TRANSITION_TYPE_REMOVE_STEWARD:
		var key types.PublicKey
		copy(key[:], t.GetRemoveSteward().GetSteward().GetRaw())
		out := current[:0]
		for _, st := range current {
			if st.Key != key {
				out = append(out, st)
			}
		}
		current = out
	}
	return current
}

func (s *State) computeCurrentThreshold(t *pb.Transition) uint32 {
	current := s.initialThreshold
	if _, ok := s.thresholdHistory[s.snapshot.Root()]; ok {
		current = s.thresholdAtLocked(t.GetPriorState())
	}
	if t.GetType() == pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD {
		current = t.GetChangeThreshold().GetNewThreshold()
	}
	return current
}

// Log returns the transitions applied to this state, in order.
func (s *State) Log() []*Transition {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Transition(nil), s.log...)
}

// =============================================================================
// Helpers
// =============================================================================

// appendOrUpdate replaces the entry for `key` (incrementing its seq), or
// appends a new one if `key` doesn't exist. If value is nil, the key is
// removed. Returns the new entries slice.
func appendOrUpdate(entries []types.StateEntry, key string, value []byte) []types.StateEntry {
	maxSeq := uint64(0)
	found := false
	for _, e := range entries {
		if e.Key == key {
			found = true
			if e.Seq > maxSeq {
				maxSeq = e.Seq
			}
		}
	}
	out := entries[:0]
	for _, e := range entries {
		if e.Key != key {
			out = append(out, e)
		}
	}
	if value != nil {
		out = append(out, types.StateEntry{
			Key:   key,
			Value: append([]byte(nil), value...),
			Seq:   maxSeq + 1,
		})
	}
	_ = found
	return out
}

// currentStewardsLocked returns the steward set at the current head.
// Used to compute the prospective steward set before applying
// ADD_STEWARD / REMOVE_STEWARD.
func currentStewardsLocked(s *State) []Steward {
	if st, ok := s.stewardHistory[s.snapshot.Root()]; ok {
		return st
	}
	return s.initialStewards
}

// prospectiveStewardsAfterAddLocked returns the steward set that WOULD
// result from adding `key` to the current steward set, with duplicates
// removed. Used to enforce the MaxStewards cap BEFORE mutating state.
func prospectiveStewardsAfterAddLocked(s *State, key types.PublicKey) []Steward {
	cur := currentStewardsLocked(s)
	out := make([]Steward, 0, len(cur)+1)
	seen := make(map[types.PublicKey]bool, len(cur)+1)
	for _, st := range cur {
		if seen[st.Key] {
			continue
		}
		seen[st.Key] = true
		out = append(out, st)
	}
	if !seen[key] {
		out = append(out, Steward{Key: key})
	}
	return out
}

// transitionTxHash returns a stable hash of the transition's canonical
// sign-bytes, used as a tiebreaker in the equivocation log. Two
// transitions with the same HLC and same canonical payload MUST have the
// same txHash; that's how we distinguish a replay (same bytes, different
// instance) from an equivocation (different bytes, same prior_state).
func transitionTxHash(t *Transition) types.Hash {
	h := sha256.Sum256(t.canonical)
	var out types.Hash
	copy(out[:], h[:])
	return out
}

// verifyOne is a tiny shim that runs the canonical signature check for
// a single (pubkey, sig) pair. Used by the equivocation log to identify
// the signing steward.
func verifyOne(pub types.PublicKey, sig types.Signature, groupKey types.GroupID, payload []byte) error {
	return crypto.Verify(pub, sig, groupKey, crypto.MsgKindTransition, payload)
}

// EncodeTransition serializes a transition for the mesh.
func EncodeTransition(t *Transition) []byte {
	b, _ := proto.Marshal(t.Proto)
	return b
}

// DecodeTransition parses a transition from the mesh. The group ID is not
// carried in the proto — callers must supply it from the envelope.
func DecodeTransition(b []byte, gid GroupID) (*Transition, error) {
	var p pb.Transition
	if err := proto.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return NewTransition(&p, gid)
}

// attestStorageKey is the storage key for an attestation. The protocol says
// attestations follow identities; we key by to-identity so a reputation
// aggregator can scan all attestations for a user.
func attestStorageKey(p *pb.AttestPayload) string {
	return "attest/" + hex.EncodeToString(p.GetToIdentity().GetRaw())
}

func binaryUint32(v uint32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return b
}

func binaryUint64(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}