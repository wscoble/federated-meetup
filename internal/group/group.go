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
//
// As of 2026-06-27, a group's state is a FOREST of branches, each
// independent. The legacy single-snapshot fields below remain for
// backward compatibility but are now REDIRECTS to branch 0 —
// callers reading state.Root() get branch 0's root, callers
// reading state.Stewards() get branch 0's stewards, etc.
//
// Cross-cutting state (mesh peers, custody declarations, equivocation
// evidence list) stays on State — these are not branch-local.
type State struct {
	groupID GroupID

	mu sync.Mutex

	// branches is the per-group branch forest. Lazily created.
	branches *branchRegistry

	// Legacy single-snapshot fields — kept so existing tests
	// continue to work without modification. Always read/write
	// branch 0. Will be removed in a future cleanup.
	snapshot         types.StateSnapshot
	stewardHistory   map[types.Hash][]Steward
	thresholdHistory map[types.Hash]uint32
	keySeq           map[string]uint64
	initialStewards  []Steward
	initialThreshold uint32
	log              []*Transition
	equivocation     *equivocationLog

	// Mesh peer registry (G2). Cross-cutting — shared across all
	// branches of the group. A mesh peer is a peer of the host,
	// not a peer of any single branch.
	meshPeers *meshPeerRegistry

	// Custody log (G3). Cross-cutting — a steward's custody tier
	// applies regardless of which branch they're operating on.
	custody *custodyLog

	// MaxStewards caps the size of the steward set per branch.
	// Apply rejects ADD_STEWARD once the prospective set would
	// exceed this cap. Default 100.
	MaxStewards int

	// Limiter rate-limits transitions per (steward, group). Cross-
	// cutting — applies across all branches. Hosts opt in via
	// SetLimiter to defend against transition flooding.
	Limiter *ratelimit.Limiter

	// maxMeshPeers caps the wg peer set size. Default 100.
	MaxMeshPeers int

	// Pending equivocation evidence (cross-cutting). Gossip'd to
	// peers; downstream consumers (SLASH_STEWARD generator) read it.
	equivocationEvidence []*EquivocationEvidence

	// MaxLogSize caps per-branch transition log size. Default 100000.
	MaxLogSize int

	// MaxKVSize caps per-branch state KV size. Default 100000.
	MaxKVSize int

	// MaxBranches caps the number of branches per group. Default
	// 1000. Past this, BRANCH_CREATE is rejected.
	MaxBranches int
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
		branches:         newBranchRegistry(),
		MaxStewards:      100,
		MaxMeshPeers:     100,
		MaxLogSize:       100000,
		MaxKVSize:        100000,
		MaxBranches:      1000,
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
		curRoot := s.snapshot.Root()
		if st, ok := s.stewardHistory[curRoot]; ok {
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

	// Branch routing. Each transition targets exactly one branch
	// (identified by its BranchId field). The transition must be
	// applied to that branch. Cross-branch transitions are nonsense
	// — the protocol is branch-local.
	branchID := BranchID(t.Proto.GetBranchId())
	targetBranch := s.branches.get(branchID)

	// CREATE_GROUP is the ONLY transition that may target a
	// non-existent branch (it creates branch 0). For all other
	// transitions targeting branch 0 that arrives before any
	// CREATE_GROUP, we lazily allocate an empty branch 0 — the
	// CREATE_GROUP itself will populate initialStewards/etc.
	if targetBranch == nil {
		if branchID == GenesisBranchID {
			targetBranch = s.branches.getOrCreate(GenesisBranchID)
		} else {
			return fmt.Errorf("group: transition targets unknown branch %d", branchID)
		}
	}

	// BRANCH_CREATE allocates a new branch. The transition itself
	// is applied to the PARENT branch (which is what the steward
	// envelope signed against); the new branch is allocated as a
	// side effect and becomes the user's working branch from here on.
	if t.Proto.GetType() == pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE {
		// Cap check: refuse if this would exceed MaxBranches.
		s.branches.mu.Lock()
		branchCount := len(s.branches.branches)
		s.branches.mu.Unlock()
		if s.MaxBranches > 0 && branchCount >= s.MaxBranches {
			return fmt.Errorf("group: BRANCH_CREATE rejected — would exceed MaxBranches=%d (current=%d)", s.MaxBranches, branchCount)
		}
	}

	// From here on, lock the target branch for branch-local ops.
	// We hold s.mu the whole time (as before) to keep the existing
	// lock discipline simple. The branch-local fields (snapshot,
	// stewardHistory, etc.) are protected by s.mu under the legacy
	// model — when we move them fully to Branch.mu, this gets more
	// nuanced. For now, branch 0's legacy fields ARE the source of
	// truth, and Apply works against them directly.
	if branchID != GenesisBranchID {
		return fmt.Errorf("group: branch-local mutations on non-genesis branches not yet wired (branch %d)", branchID)
	}

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
	// kvAllowed is set by each appendOrUpdate call. If a call would
	// exceed MaxKVSize, the transition is rejected with ErrKVSizeExceeded.
	var kvAllowed bool
	newEntries := append([]types.StateEntry(nil), s.snapshot.Entries...)
	switch t.Proto.GetType() {
	case pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP:
		p := t.Proto.GetCreateGroup()
		newEntries, kvAllowed = appendOrUpdate(newEntries, "name", []byte(p.GetCanonicalName()), s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
		newEntries, kvAllowed = appendOrUpdate(newEntries, "display_name", []byte(p.GetDisplayName()), s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
		// Initial stewards are written into the Merkle KV so that the
		// snapshot root commits to the steward set. Without this,
		// REMOVE_STEWARD on an initial steward would be a no-op against
		// the snapshot (the entry doesn't exist) and the state root
		// would not advance — leaving the root out of sync with the
		// stewardHistory, which violates the Merkle commitment invariant
		// that two hosts with the same root must agree on the steward
		// set. Now both ADD_STEWARD and REMOVE_STEWARD operate on
		// entries that are always present.
		for _, k := range p.GetInitialStewards() {
			newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("steward/%x", k.GetRaw()), []byte{1}, s.MaxKVSize)
			if !kvAllowed { return ErrKVSizeExceeded }
		}
		// Bootstrap seed: initial mesh peers declared at group
		// creation. This closes the chicken-and-egg gap where
		// ADD_HOST_PEER requires an existing mesh member to co-sign
		// but the mesh starts empty. Founding stewards declare the
		// first mesh members; subsequent ADD_HOST_PEER uses those
		// peers as cosigners.
		//
		// The peer is added to the in-memory meshPeers registry AND
		// recorded in the Merkle KV (mesh_peer/<hex> = mesh_ip) so
		// mirrors replaying CREATE_GROUP see the same membership.
		for _, mp := range p.GetInitialMeshPeers() {
			// InitialMeshPeer.host_wg_key is bytes (X25519, 32 bytes).
			// MeshPeer.HostWGKey is the proto PublicKey wrapper.
			peer := &MeshPeer{
				HostWGKey: pb.PublicKey{Raw: append([]byte(nil), mp.GetHostWgKey()...)},
				MeshIP:    append([]byte(nil), mp.GetMeshIp()...),
			}
			if err := s.addMeshPeerLocked(peer); err != nil {
				return fmt.Errorf("group: CREATE_GROUP initial_mesh_peers: %w", err)
			}
			newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("mesh_peer/%x", mp.GetHostWgKey()), mp.GetMeshIp(), s.MaxKVSize)
			if !kvAllowed { return ErrKVSizeExceeded }
		}
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
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("steward/%x", key[:]), []byte{1}, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_REMOVE_STEWARD:
		p := t.Proto.GetRemoveSteward()
		var key types.PublicKey
		copy(key[:], p.GetSteward().GetRaw())
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("steward/%x", key[:]), nil, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_CHANGE_THRESHOLD:
		p := t.Proto.GetChangeThreshold()
		newEntries, kvAllowed = appendOrUpdate(newEntries, "threshold", binaryUint32(p.GetNewThreshold()), s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_ADD_MEMBER:
		p := t.Proto.GetAddMember()
		var user types.PublicKey
		copy(user[:], p.GetUser().GetRaw())
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("member/%x", user[:]), []byte{1}, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_REMOVE_MEMBER:
		p := t.Proto.GetRemoveMember()
		var user types.PublicKey
		copy(user[:], p.GetUser().GetRaw())
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("member/%x", user[:]), nil, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_CREATE_EVENT:
		p := t.Proto.GetCreateEvent()
		// Store the event payload as protobuf bytes keyed by event_id.
		ep, err := proto.Marshal(p)
		if err != nil {
			return fmt.Errorf("group: marshal event: %w", err)
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries, "event/"+p.GetEventId(), ep, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_UPDATE_EVENT:
		// PATCH semantics: store the patch keyed by event_id under
		// event_patch/{id}. Hosts apply patches on read; the state
		// machine records the patch's existence and ordering via HLC.
		p := t.Proto.GetUpdateEvent()
		pp, err := proto.Marshal(p)
		if err != nil {
			return fmt.Errorf("group: marshal event patch: %w", err)
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries, "event_patch/"+p.GetEventId(), pp, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_CANCEL_EVENT:
		p := t.Proto.GetCancelEvent()
		newEntries, kvAllowed = appendOrUpdate(newEntries, "event_cancelled/"+p.GetEventId(), []byte{1}, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_RSVP:
		p := t.Proto.GetRsvp()
		var user types.PublicKey
		copy(user[:], p.GetUser().GetRaw())
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("rsvp/%s/%x", p.GetEventId(), user[:]), []byte{1}, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_CANCEL_RSVP:
		p := t.Proto.GetCancelRsvp()
		var user types.PublicKey
		copy(user[:], p.GetUser().GetRaw())
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("rsvp/%s/%x", p.GetEventId(), user[:]), nil, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_ATTEST:
		p := t.Proto.GetAttest()
		attestKey := attestStorageKey(p)
		attestBytes, err := proto.Marshal(p)
		if err != nil {
			return fmt.Errorf("group: marshal attest: %w", err)
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries, attestKey, attestBytes, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_FORK:
		// Fork creates a NEW group; the parent group's state machine just
		// records the fork line. The new group is built separately.
		p := t.Proto.GetFork()
		newEntries, kvAllowed = appendOrUpdate(newEntries, "fork_lineage", []byte(p.GetNewGroupKey().GetRaw()), s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
	case pb.TransitionType_TRANSITION_TYPE_MIGRATE:
		p := t.Proto.GetMigrate()
		newEntries, kvAllowed = appendOrUpdate(newEntries, "canonical_host", []byte(p.GetNewHost()), s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
		newEntries, kvAllowed = appendOrUpdate(newEntries, "canonical_after", binaryUint64(uint64(p.GetDeadline().GetSeconds())), s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }

	// =====================================================================
	// G1 — Host certificate issuance / revocation. Surface: TLS layer.
	// Gate: kills the public-CA attack surface by moving cert issuance
	// into the protocol. Only stewards (whoever M-of-N of them are)
	// authorize which TLS keys serve which hostnames.
	// =====================================================================
	case pb.TransitionType_TRANSITION_TYPE_ISSUE_HOST_CERT:
		p := t.Proto.GetIssueHostCert()
		if p == nil {
			return errors.New("group: ISSUE_HOST_CERT missing payload")
		}
		// Encode the cert as canonical bytes under a deterministic key.
		// Multiple certs per host are allowed (hostname may change, or
		// the host may rotate TLS keys). The (hostname, host_tls_key,
		// not_after) tuple is the unique identifier.
		certBytes, err := proto.Marshal(p)
		if err != nil {
			return fmt.Errorf("group: marshal issue_host_cert: %w", err)
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries, hostCertStorageKey(p), certBytes, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }

	case pb.TransitionType_TRANSITION_TYPE_REVOKE_HOST_CERT:
		p := t.Proto.GetRevokeHostCert()
		if p == nil {
			return errors.New("group: REVOKE_HOST_CERT missing payload")
		}
		// Revocation: tombstone the cert entry. Hosts MUST drop any
		// cached cert that has a matching revocation in their state.
		revBytes, err := proto.Marshal(p)
		if err != nil {
			return fmt.Errorf("group: marshal revoke_host_cert: %w", err)
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries, hostCertRevocationKey(p), revBytes, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
		// Also tombstone the cert entry itself — clients seeing both
		// can correlate.
		newEntries, kvAllowed = appendOrUpdate(newEntries, hostCertStorageKeyFromRevoke(p), nil, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }

	// =====================================================================
	// G2 — WireGuard mesh peer admission. Surface: mesh transport.
	// Gate: kills the rogue-bootstrap attack. ADD_HOST_PEER requires
	// steward threshold signatures AND a co-signature from an
	// existing mesh member.
	// =====================================================================
	case pb.TransitionType_TRANSITION_TYPE_ADD_HOST_PEER:
		p := t.Proto.GetAddHostPeer()
		if p == nil {
			return errors.New("group: ADD_HOST_PEER missing payload")
		}
		if err := verifyAddHostPeerPayload(s, p); err != nil {
			return err
		}
		newPeer := &MeshPeer{
			HostWGKey: *p.HostWgKey,
			MeshIP:    append([]byte(nil), p.GetMeshIp()...),
		}
		// Cap the mesh size (G4). Reject if the prospective count
		// exceeds MaxMeshPeers.
		if s.MaxMeshPeers > 0 {
			current := 0
			if s.meshPeers != nil {
				current = s.meshPeers.Len()
			}
			if current+1 > s.MaxMeshPeers {
				return fmt.Errorf("group: ADD_HOST_PEER rejected — mesh peer count would grow to %d, MaxMeshPeers=%d", current+1, s.MaxMeshPeers)
			}
		}
		if err := s.addMeshPeerLocked(newPeer); err != nil {
			return err
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("mesh_peer/%x", p.GetHostWgKey().GetRaw()), p.GetMeshIp(), s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }

	case pb.TransitionType_TRANSITION_TYPE_REMOVE_HOST_PEER:
		p := t.Proto.GetRemoveHostPeer()
		if p == nil {
			return errors.New("group: REMOVE_HOST_PEER missing payload")
		}
		removed := &MeshPeer{
			HostWGKey: *p.HostWgKey,
			MeshIP:    append([]byte(nil), p.GetMeshIp()...),
		}
		if err := s.removeMeshPeerLocked(removed); err != nil {
			return err
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("mesh_peer/%x", p.GetHostWgKey().GetRaw()), nil, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }

	// =====================================================================
	// G3 — Steward custody declaration. Surface: multisig weight.
	// Gate: lets threshold policy require "M of N must be HSM-or-better".
	// =====================================================================
	case pb.TransitionType_TRANSITION_TYPE_DECLARE_STEWARD_CUSTODY:
		p := t.Proto.GetDeclareStewardCustody()
		if p == nil {
			return errors.New("group: DECLARE_STEWARD_CUSTODY missing payload")
		}
		if err := verifyDeclareStewardCustody(s, p); err != nil {
			return err
		}
		s.recordCustodyLocked(CustodyDeclaration{
			Steward: *p.Steward,
			Tier:    p.GetTier(),
		})
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("custody/%x", p.GetSteward().GetRaw()), []byte{byte(p.GetTier())}, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }

	// =====================================================================
	// G6 — Auto-slash for equivocation. Detection (already in place)
	// becomes action: when evidence is published, the threshold of
	// OTHER stewards can sign a SLASH_STEWARD transition that removes
	// the offending key. The slashed steward cannot co-sign.
	// =====================================================================
	case pb.TransitionType_TRANSITION_TYPE_SLASH_STEWARD:
		p := t.Proto.GetSlashSteward()
		if p == nil {
			return errors.New("group: SLASH_STEWARD missing payload")
		}
		if err := verifySlashStewardPayload(s, p, t); err != nil {
			return err
		}
		// Apply the slash by recording an evidence entry and removing
		// the steward from the current set. The slashed steward MUST
		// not be a signer; the threshold of OTHER stewards authored
		// the slash.
		slashedKey := types.PublicKey{}
		copy(slashedKey[:], p.GetSlashedSteward().GetRaw())
		// Record evidence in state for downstream consumers.
		ev := &EquivocationEvidence{
			GroupID:    s.groupID,
			StewardKey: slashedKey,
			PriorState: types.Hash{},
		}
		copy(ev.PriorState[:], p.GetPriorState().GetHash())
		s.equivocationEvidence = append(s.equivocationEvidence, ev)
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("steward/%x", slashedKey[:]), nil, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
		newEntries, kvAllowed = appendOrUpdate(newEntries, fmt.Sprintf("slashed/%x", slashedKey[:]), []byte{1}, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }
		// SLASH_STEWARD also mutates the steward set (removes the
		// slashed key), so we mark it for the post-Apply steward-set
		// recompute via the standard steward-mutation path. The
		// post-Apply call to computeCurrentStewards handles this
		// automatically since it walks back via prior_state. We just
		// need to make sure the slashed key is NOT in the multisig
// =====================================================================
	// Branch-local mutation: BRANCH_CREATE. The transition itself is
	// recorded against the parent branch (the steward envelope
	// verifies against the parent's stewards); the NEW branch is
	// allocated as a side effect, inheriting the parent's stewards
	// and threshold at the snapshot.
	// =====================================================================
	case pb.TransitionType_TRANSITION_TYPE_BRANCH_CREATE:
		p := t.Proto.GetBranchCreate()
		if p == nil {
			return errors.New("group: BRANCH_CREATE missing payload")
		}
		// Capture the parent's current stewards + threshold BEFORE
		// we mutate state.
		parentStewards := s.stewardsAtLocked(t.Proto.GetPriorState())
		parentThreshold := s.thresholdAtLocked(t.Proto.GetPriorState())
		// Allocate the new branch.
		newBranch := s.branches.allocate(BranchID(branchID), p.GetReason())
		newBranch.initialStewards = append([]Steward(nil), parentStewards...)
		newBranch.initialThreshold = parentThreshold
		// Record genesis HLC from this transition.
		newBranch.genesisHLC = append([]byte(nil), t.Proto.GetHlc()...)
		// Record the branch creation in the PARENT branch's KV
		// (so mirrors replaying the parent see the branch exist).
		newEntries, kvAllowed = appendOrUpdate(newEntries,
			fmt.Sprintf("branch/%d/parent", newBranch.id),
			[]byte(fmt.Sprintf("%d", branchID)),
			s.MaxKVSize,
		)
		if !kvAllowed {
			return ErrKVSizeExceeded
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries,
			fmt.Sprintf("branch/%d/reason", newBranch.id),
			[]byte(p.GetReason()),
			s.MaxKVSize,
		)
		if !kvAllowed {
			return ErrKVSizeExceeded
		}

	// =====================================================================
	// G8 — Discovery binding. Surface: directory lookup.
	// Gate: phishing a name requires forging a steward threshold
	// signature on a NAME_BIND transition.
	// =====================================================================
	case pb.TransitionType_TRANSITION_TYPE_NAME_BIND:
		p := t.Proto.GetNameBind()
		if p == nil {
			return errors.New("group: NAME_BIND missing payload")
		}
		if err := verifyNameBindPayload(s, p); err != nil {
			return err
		}
		newEntries, kvAllowed = appendOrUpdate(newEntries, nameBindStorageKey(p), []byte{1}, s.MaxKVSize)
		if !kvAllowed { return ErrKVSizeExceeded }

	default:
		return fmt.Errorf("group: unsupported transition type %v", t.Proto.GetType())
	}

	s.snapshot = types.StateSnapshot{Entries: newEntries}
	stewardsAfterApply := s.computeCurrentStewards(t.Proto)
	r := s.snapshot.Root()
	s.stewardHistory[r] = stewardsAfterApply
	s.thresholdHistory[r] = s.computeCurrentThreshold(t.Proto)
	s.log = append(s.log, t)
	// G7 memory bound on transition log. When the log exceeds
	// MaxLogSize, evict the oldest entry. Eviction is purely local;
	// hosts that need full history for audit use persistent storage.
	if s.MaxLogSize > 0 && len(s.log) > s.MaxLogSize {
		// Drop in chunks to amortize the slice copy.
		drop := len(s.log) - s.MaxLogSize
		s.log = append([]*Transition{}, s.log[drop:]...)
	}
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
	case pb.TransitionType_TRANSITION_TYPE_CREATE_GROUP:
		// After CREATE_GROUP, the steward set IS the initial stewards
		// declared in the payload (set by Apply at the verification
		// step). The "current" lookup above walks via prior_state,
		// which for a fresh group is the zero hash — that path doesn't
		// find anything. Use the field directly.
		return append([]Steward(nil), s.initialStewards...)
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
	case pb.TransitionType_TRANSITION_TYPE_SLASH_STEWARD:
		// SLASH_STEWARD removes the slashed key from the active
		// set, identical to REMOVE_STEWARD for the steward-set
		// computation. The verify* gate ensures the slashed
		// steward did not co-sign their own removal.
		var key types.PublicKey
		copy(key[:], t.GetSlashSteward().GetSlashedSteward().GetRaw())
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

// ErrKVSizeExceeded is returned by Apply when a transition would
// cause the per-branch state KV to exceed MaxKVSize. The transition
// is rejected and no state changes occur.
var ErrKVSizeExceeded = &groupError{Kind: "kv_size_exceeded", Msg: "state KV would exceed MaxKVSize"}

// appendOrUpdate replaces the entry for `key` (incrementing its seq), or
// appends a new one if `key` doesn't exist. If value is nil, the key is
// removed. Returns the new entries slice and a flag indicating whether
// the append was allowed.
//
// G7 memory bound: when the entries slice exceeds `maxSize`, the call
// returns the unchanged entries with allowed=false. Callers should
// reject the transition with ErrKVSizeExceeded.
//
// We return a flag rather than an error to keep appendOrUpdate
// allocation-free; the caller (Apply) maps the flag to an error.
func appendOrUpdate(entries []types.StateEntry, key string, value []byte, maxSize int) ([]types.StateEntry, bool) {
	if maxSize > 0 && value != nil && len(entries) >= maxSize {
		found := false
		for _, e := range entries {
			if e.Key == key {
				found = true
				break
			}
		}
		if !found {
			return entries, false
		}
	}
	return appendOrUpdateUnchecked(entries, key, value), true
}

// appendOrUpdateUnchecked is the inner implementation without the
// G7 cap check. Used internally; callers should use appendOrUpdate.
func appendOrUpdateUnchecked(entries []types.StateEntry, key string, value []byte) []types.StateEntry {
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