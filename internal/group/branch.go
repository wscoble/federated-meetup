// SPDX-License-Identifier: MIT
//
// Branch-local state machine (protocol §3, post-2026-06-27).
//
// A group's state is a FOREST of branches, not a single log. Each
// branch is an independent state machine with its own:
//
//   - Merkle KV state
//   - Transition log
//   - Equivocation log
//   - Steward history
//   - Threshold history
//
// Branches share the group's keypair (so cross-branch messages
// verify) and the cross-cutting registry (mesh peers, custody
// declarations, equivocation evidence list). They do NOT share
// state mutations.
//
// Branch 0 is the genesis branch — created by CREATE_GROUP, holds
// the initial steward set and threshold. Subsequent branches are
// created by BRANCH_CREATE and inherit the parent's steward set at
// the snapshot point.
//
// IDs are monotonic uint32. Never reused within a group's lifetime.

package group

import (
	"sync"

	"github.com/sscoble/federated-meetup/internal/types"
	pb "github.com/sscoble/federated-meetup/proto/federated_meetup/v1"
)

// BranchID is the canonical identifier of a branch within a group.
// Zero is the genesis branch.
type BranchID uint32

// GenesisBranchID is branch 0 — the branch created by CREATE_GROUP.
const GenesisBranchID BranchID = 0

// Branch is the per-branch state. All branch-local data lives here.
type Branch struct {
	id BranchID

	// mu guards everything below. State.mu guards the map of
	// branches + cross-cutting registries. Branch.mu guards the
	// per-branch state.
	mu sync.Mutex

	// snapshot is the branch's current Merkle KV state.
	snapshot types.StateSnapshot

	// stewardHistory maps state root → steward set at that root.
	stewardHistory map[types.Hash][]Steward

	// thresholdHistory maps state root → threshold at that root.
	thresholdHistory map[types.Hash]uint32

	// keySeq tracks per-key sequence numbers within this branch.
	keySeq map[string]uint64

	// initialStewards is the steward set at branch genesis.
	// Branch 0's initial stewards come from CREATE_GROUP.
	// Non-zero branches inherit from their parent at the snapshot.
	initialStewards []Steward
	initialThreshold uint32

	// log is the per-branch transition log.
	//lint:ignore U1000 reserved for future use
	log []*Transition

	// equivocation tracks (steward, prior_state) within this branch.
	// Cross-branch equivocation is meaningless — different branches
	// are different state machines.
	//lint:ignore U1000 reserved for future use
	equivocation *equivocationLog

	// transitionCount is incremented on every Apply. Surfaced via
	// BranchInfo for BRANCH_LIST queries.
	transitionCount uint64

	// parentBranchID is the branch this one was created from.
	// Zero for genesis.
	parentBranchID BranchID

	// genesisHLC is the HLC of the first transition on this branch.
	genesisHLC []byte

	// reason is the BRANCH_CREATE rationale. Empty for genesis.
	reason string
}

// newBranch constructs an empty branch with no transitions.
func newBranch(id BranchID) *Branch {
	return &Branch{
		id:               id,
		stewardHistory:   make(map[types.Hash][]Steward),
		thresholdHistory: make(map[types.Hash]uint32),
		keySeq:           make(map[string]uint64),
	}
}

// ID returns the branch identifier.
func (b *Branch) ID() BranchID { return b.id }

// Root returns the branch's current state root.
func (b *Branch) Root() types.Hash {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshot.Root()
}

// Snapshot returns the branch's current state snapshot.
func (b *Branch) Snapshot() types.StateSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshot
}

// TransitionCount returns the number of transitions applied to
// this branch. Surfaced via BRANCH_LIST.
func (b *Branch) TransitionCount() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.transitionCount
}

// ParentBranchID returns the branch this one was created from.
func (b *Branch) ParentBranchID() BranchID {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.parentBranchID
}

// GenesisHLC returns the HLC of the first transition on this branch.
func (b *Branch) GenesisHLC() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.genesisHLC...)
}

// Reason returns the BRANCH_CREATE rationale. Empty for genesis.
func (b *Branch) Reason() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reason
}

// Head returns a *pb.StateRoot for this branch's current head.
func (b *Branch) Head() *pb.StateRoot {
	b.mu.Lock()
	defer b.mu.Unlock()
	root := b.snapshot.Root()
	return &pb.StateRoot{Hash: root[:]}
}

// InitialStewards returns the steward set captured at branch
// creation time. This is the snapshot the branch starts with; it
// does NOT track subsequent changes to the parent branch. For the
// live steward set at the branch's current head, use Branch.StewardsAt(nil).
func (b *Branch) InitialStewards() []Steward {
	return append([]Steward(nil), b.initialStewards...)
}

// InitialThreshold returns the threshold captured at branch creation
// time. Snapshot semantics — does not track parent changes. For the
// live threshold, use Branch.ThresholdAt(nil).
func (b *Branch) InitialThreshold() uint32 {
	return b.initialThreshold
}


// =============================================================================
// Branch registry — group-level
// =============================================================================

// branches tracks all branches for a group. Lazily populated; an
// empty State has no branches (the first CREATE_GROUP creates
// branch 0).
type branchRegistry struct {
	mu       sync.Mutex
	branches map[BranchID]*Branch
	// nextID is the next branch ID to allocate. Starts at 1
	// (genesis is 0).
	nextID BranchID
}

func newBranchRegistry() *branchRegistry {
	return &branchRegistry{
		branches: make(map[BranchID]*Branch),
		nextID:   1,
	}
}

// get returns the branch with the given ID, or nil.
func (r *branchRegistry) get(id BranchID) *Branch {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.branches[id]
}

// getOrCreate returns the branch with the given ID, creating it if
// it doesn't exist. Used during initial sync / replay.
func (r *branchRegistry) getOrCreate(id BranchID) *Branch {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.branches[id]
	if !ok {
		b = newBranch(id)
		r.branches[id] = b
	}
	return b
}

// allocate creates a new branch with the next available ID.
// Genesis (id=0) must be allocated via allocateGenesis.
func (r *branchRegistry) allocate(parent BranchID, reason string) *Branch {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.nextID
	r.nextID++
	b := newBranch(id)
	b.parentBranchID = parent
	b.reason = reason
	r.branches[id] = b
	return b
}

// list returns BranchInfo for every branch, sorted by ID ascending.
func (r *branchRegistry) list() []*pb.BranchInfo {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	// Collect IDs first to avoid holding r.mu while locking each branch.
	ids := make([]BranchID, 0, len(r.branches))
	for id := range r.branches {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	// Sort.
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	out := make([]*pb.BranchInfo, 0, len(ids))
	for _, id := range ids {
		r.mu.Lock()
		b := r.branches[id]
		r.mu.Unlock()
		if b == nil {
			continue
		}
		b.mu.Lock()
		root := b.snapshot.Root()
		bi := &pb.BranchInfo{
			BranchId:        uint32(b.id),
			Head:            &pb.StateRoot{Hash: root[:]},
			ParentBranchId:  uint32(b.parentBranchID),
			GenesisHlc:      append([]byte(nil), b.genesisHLC...),
			TransitionCount: b.transitionCount,
			Reason:          b.reason,
		}
		b.mu.Unlock()
		out = append(out, bi)
	}
	return out
}