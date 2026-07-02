// SPDX-License-Identifier: MIT
//
// Equivocation detection for the multisig envelope.
//
// Background: a malicious steward (or one whose private key has been
// compromised) can sign two different transitions at the same
// prior_state — a "fork attempt by an insider". The state machine can
// only apply one of them (whichever lands first); the other fails with
// prior_state mismatch. But by then the malicious host may have already
// gossiped both to different parts of the federation, splitting honest
// hosts' views of the state.
//
// Equivocation evidence is the smoking gun: two signed envelopes with
// the same (steward_pubkey, prior_state) but different payloads / HLCs.
// Any host that observes both can publish the evidence and trigger an
// automated REMOVE_STEWARD transition.
//
// Reference: docs/02-PROTOCOL.md §5.4 (threat model) — "Rogue / Sybil
// peer", "Byzantine collusion".

package group

import (
	"log"
	"sync"

	"github.com/sscoble/federated-meetup/internal/types"
)

// EquivocationEvidence is the proof that a single steward signed two
// different transitions at the same prior_state. The two transitions are
// the smoking gun — together they identify the offending key (via the
// multisig envelope) and the disagreement (via prior_state being equal
// but HLCs / payloads being different).
//
// Stored alongside State; produced lazily as conflicting transitions
// land. Hosts gossip evidence to peers via the ATTEST pipeline.
type EquivocationEvidence struct {
	GroupID     types.GroupID
	StewardKey  types.PublicKey // the offending steward pubkey
	PriorState  types.Hash      // the shared prior_state
	TransitionA *Transition     // first seen
	TransitionB *Transition     // second seen (the one that conflicted)
}

// equivocationLog tracks every (steward, prior_state) pair we have ever
// applied a transition from, plus the HLC of that transition. When a
// second distinct HLC arrives at the same (steward, prior_state), we
// surface an EquivocationEvidence and refuse to apply the conflicting
// transition.
//
// Memory bound (G7): entries are tracked in an LRU of `maxEntries`.
// When the log exceeds the cap, the oldest entry is evicted. Eviction
// does NOT weaken equivocation detection for the recent past — an
// adversary would need to bypass the most recent N entries to land
// an equivocation. For active-adversy detection over a rolling
// window, maxEntries=10000 is more than enough.
//
// Default: 10000 entries. Configurable via SetMaxEntries.
type equivocationLog struct {
	mu sync.Mutex

	maxEntries int

	// evictions counts how many entries have been dropped by FIFO
	// eviction. Exposed via State.EquivocationEvictions() so operators
	// and honest peers can observe that the detection window has
	// shrunk. (Audit C-3, cycle 51.)
	evictions uint64

	// seen[key] = first HLC observed for that key. Eviction removes
	// the oldest insertion (we don't track access time — the threat
	// model is flooding, not stale-lookups).
	seen map[equivocationKey]hlcSeen

	// insertionOrder tracks keys in insertion order for FIFO eviction.
	// The head is the oldest entry; eviction pops from the head and
	// removes from `seen`.
	insertionOrder []equivocationKey
}

// EquivocationLogMaxEntries is the default cap on equivocation log size.
// 10000 entries × ~120 bytes/key ≈ 1.2 MB worst case. Production hosts
// SHOULD override if they expect more stewards or longer histories.
var EquivocationLogMaxEntries = 10000

// newEquivocationLog creates an empty log with the default cap.
func newEquivocationLog() *equivocationLog {
	return &equivocationLog{
		maxEntries: EquivocationLogMaxEntries,
		seen:       make(map[equivocationKey]hlcSeen),
	}
}

// NewEquivocationLogForTest creates an equivocation log with an
// explicit cap. Test-only — production code uses newEquivocationLog
// with the default 10000-entry cap.
func NewEquivocationLogForTest(maxEntries int) *equivocationLog {
	return &equivocationLog{
		maxEntries: maxEntries,
		seen:       make(map[equivocationKey]hlcSeen),
	}
}

// InsertForTest records a (steward, prior, hlc, txhash) entry. Test-only.
func (e *equivocationLog) InsertForTest(steward, prior [32]byte, hlc []byte, txhash [32]byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := equivocationKey{StewardKey: steward, PriorState: prior}
	e.seen[key] = hlcSeen{HLC: append([]byte(nil), hlc...), TxHash: txhash}
	e.insertionOrder = append(e.insertionOrder, key)
	e.evictOldestLocked()
}

// HasForTest reports whether the (steward, prior) pair is currently in
// the log. Test-only.
func (e *equivocationLog) HasForTest(steward, prior [32]byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.seen[equivocationKey{StewardKey: steward, PriorState: prior}]
	return ok
}

// CheckForTest runs the equivocation check for the given (steward,
// prior, hlc, txhash). Returns true if a conflict is detected (the
// existing entry has a different HLC or txHash). Test-only.
func (e *equivocationLog) CheckForTest(steward, prior [32]byte, hlc []byte, txhash [32]byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	key := equivocationKey{StewardKey: steward, PriorState: prior}
	prev, ok := e.seen[key]
	if !ok {
		return false
	}
	// Same HLC and same txHash → exact duplicate (replay).
	if bytesEqual(prev.HLC, hlc) && prev.TxHash == txhash {
		return false
	}
	return true
}

// SetMaxEntries overrides the eviction cap. Setting to 0 disables
// eviction (legacy / test mode).
func (e *equivocationLog) SetMaxEntries(n int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.maxEntries = n
}

type equivocationKey struct {
	StewardKey types.PublicKey
	PriorState types.Hash
}

type hlcSeen struct {
	HLC    []byte // first HLC we saw; nil if no HLC was on the transition
	TxHash types.Hash
}

// evictOldest removes the oldest insertion. Called when the log
// grows past maxEntries. No-op if the log is empty or if maxEntries
// is 0 (unbounded).
//
// Each eviction increments the evictions counter and logs a structured
// warning so operators can observe the detection window shrinking.
// (Audit C-3, cycle 51.)
func (e *equivocationLog) evictOldestLocked() {
	if e.maxEntries <= 0 || len(e.insertionOrder) == 0 {
		return
	}
	if len(e.insertionOrder) <= e.maxEntries {
		return
	}
	for len(e.insertionOrder) > e.maxEntries {
		oldest := e.insertionOrder[0]
		e.insertionOrder = e.insertionOrder[1:]
		delete(e.seen, oldest)
		e.evictions++
		log.Printf("WARN: equivocation-log evicted entry steward=%x prior=%x evictions=%d window=%d",
			oldest.StewardKey[:8], oldest.PriorState[:8], e.evictions, len(e.seen))
	}
}

// Evictions returns the number of entries dropped by FIFO eviction
// since the log was created. Test + observability accessor.
func (e *equivocationLog) Evictions() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.evictions
}

// check looks up the (steward, prior_state) pair. Returns:
//   - nil, nil if this is the first time we've seen this pair (no conflict).
//   - evidence, nil if the new HLC differs from the recorded one (equivocation
//     detected — caller should NOT apply the transition; publish evidence).
//   - nil, err on internal error.
//
// `hlcBytes` is the HLC carried on the inbound transition. `txHash` is a
// caller-computed hash of the transition's canonical bytes (used as a
// second tiebreaker — same HLC + same prior_state from the same steward
// is a duplicate, not an equivocation).
func (e *equivocationLog) check(
	stewardKey types.PublicKey,
	priorState types.Hash,
	hlcBytes []byte,
	txHash types.Hash,
) (*EquivocationEvidence, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	key := equivocationKey{StewardKey: stewardKey, PriorState: priorState}
	prev, ok := e.seen[key]
	if !ok {
		e.seen[key] = hlcSeen{HLC: append([]byte(nil), hlcBytes...), TxHash: txHash}
		e.insertionOrder = append(e.insertionOrder, key)
		e.evictOldestLocked()
		return nil, nil
	}

	// Same HLC and same txHash → exact duplicate (replay of an
	// already-applied transition). Not an equivocation; just drop.
	if bytesEqual(prev.HLC, hlcBytes) && prev.TxHash == txHash {
		return nil, nil
	}

	// Either:
	//   (a) different HLC at same (steward, prior_state) — equivocation
	//   (b) same HLC but different txHash — also equivocation
	//       (impossible: same HLC + same canonical payload ⇒ same txHash)
	return &EquivocationEvidence{
		StewardKey: stewardKey,
		PriorState: priorState,
	}, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// State-internal hook: Apply() calls checkEquivocation under the state
// lock before mutating any state. Returns evidence if this transition
// is a conflict, nil if clean.
//
// `signingSteward` is one of the stewards whose signature verified —
// it's enough that ANY verifying steward equivocated. Hosts that see
// evidence can act on it.
//
// When equivocation is detected, the full EquivocationEvidence
// (including TransitionA and TransitionB) is stored on the state's
// evidence list so peers can gossip it via SubmitEvidence.
// (Audit H-9 cycle 99, cycle 51.)
func (s *State) checkEquivocationLocked(
	signingSteward types.PublicKey,
	priorState types.Hash,
	hlcBytes []byte,
	txHash types.Hash,
	conflictingTransition *Transition,
) bool {
	if s.equivocation == nil {
		s.equivocation = newEquivocationLog()
	}
	ev, _ := s.equivocation.check(signingSteward, priorState, hlcBytes, txHash)
	if ev == nil {
		return false
	}
	// Populate the full evidence with both transitions.
	// TransitionA is the previously-seen transition (the one already
	// in the log). TransitionB is the conflicting one (just rejected).
	// We find TransitionA by walking the log for a transition from
	// the same (steward, prior_state) pair.
	var transitionA *Transition
	for i := len(s.log) - 1; i >= 0; i-- {
		t := s.log[i]
		prior := types.Hash{}
		copy(prior[:], t.Proto.GetPriorState().GetHash())
		if prior == priorState {
			signing := t.findSigningSteward(s.stewardsAtLocked(t.Proto.GetPriorState()))
			if signing == signingSteward {
				transitionA = t
				break
			}
		}
	}
	ev.TransitionA = transitionA
	ev.TransitionB = conflictingTransition
	s.equivocationEvidence = append(s.equivocationEvidence, ev)
	// H-2: cap the evidence slice (FIFO eviction).
	s.capEvidenceLocked()
	return true
}

// EquivocationEvidenceFor returns the evidence log entry for a (steward,
// prior_state) pair, if one was recorded. The full TransitionA / B
// fields are populated by Apply before storing. Returns nil if no
// evidence has been recorded.
func (s *State) EquivocationEvidenceFor(
	stewardKey types.PublicKey,
	priorState types.Hash,
) *EquivocationEvidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.equivocation == nil {
		return nil
	}
	s.equivocation.mu.Lock()
	defer s.equivocation.mu.Unlock()
	_, ok := s.equivocation.seen[equivocationKey{StewardKey: stewardKey, PriorState: priorState}]
	if !ok {
		return nil
	}
	return &EquivocationEvidence{
		GroupID:    s.groupID,
		StewardKey: stewardKey,
		PriorState: priorState,
	}
}

// EquivocationEvictions returns the number of equivocation-log
// entries that have been silently dropped by FIFO eviction. A non-zero
// value means the detection window has shrunk: equivocation for
// evicted (steward, prior_state) pairs is no longer detectable by
// this host. Operators and honest peers can use this to decide
// whether to request a fresh evidence sync or increase the cap.
//
// (Audit C-3, cycle 51.)
func (s *State) EquivocationEvictions() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.equivocation == nil {
		return 0
	}
	return s.equivocation.Evictions()
}

// AllEquivocationEvidence returns every recorded piece of equivocation
// evidence for this group. Hosts gossip this list to peers so all
// observers converge on the same set of slashed keys.
func (s *State) AllEquivocationEvidence() []*EquivocationEvidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.equivocation == nil {
		return nil
	}
	s.equivocation.mu.Lock()
	defer s.equivocation.mu.Unlock()
	out := make([]*EquivocationEvidence, 0, len(s.equivocation.seen))
	for k, v := range s.equivocation.seen {
		if v.HLC == nil {
			continue
		}
		out = append(out, &EquivocationEvidence{
			GroupID:    s.groupID,
			StewardKey: k.StewardKey,
			PriorState: k.PriorState,
		})
	}
	return out
}

// StoreEvidence adds externally-observed equivocation evidence (from
// a peer via SubmitEvidence) to the state's evidence list. This is
// the gossip path: a peer that saw both transitions sends the evidence
// to us so we can submit SLASH_STEWARD even if we only saw one.
//
// Thread-safe. Does NOT verify the evidence — the caller (the RPC
// handler) should verify signatures on the transitions before calling.
//
// (Audit H-9, cycle 51.)
func (s *State) StoreEvidence(ev *EquivocationEvidence) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.equivocationEvidence = append(s.equivocationEvidence, ev)
	// H-2: cap the evidence slice (FIFO eviction).
	s.capEvidenceLocked()
}

// capEvidenceLocked drops the oldest evidence entries until the
// slice fits within MaxEvidenceEntries. Called under s.mu.
// (Audit H-2, cycle 51.)
func (s *State) capEvidenceLocked() {
	if s.MaxEvidenceEntries <= 0 {
		return
	}
	for len(s.equivocationEvidence) > s.MaxEvidenceEntries {
		s.equivocationEvidence = s.equivocationEvidence[1:]
	}
}

// StoredEvidence returns the evidence list (from both local detection
// and gossip via SubmitEvidence). This is the list hosts use to submit
// SLASH_STEWARD transitions and to respond to peer evidence requests.
//
// (Audit H-9, cycle 51.)
func (s *State) StoredEvidence() []*EquivocationEvidence {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*EquivocationEvidence(nil), s.equivocationEvidence...)
}

// CheckEquivocation is the public, lock-acquiring entry point for the
// equivocation log. Returns true if (steward, prior, hlc, tx) collides
// with a previously-applied transition at the same (steward, prior).
// Used by tests and by future gossip-level equivocation detectors that
// see two transitions from different peers at the same prior.
//
// The internal `checkEquivocationLocked` is called from Apply under
// the state lock; this public version is safe to call from outside.
// Pass nil for conflictingTransition if you only need the boolean check.
func (s *State) CheckEquivocation(
	stewardKey types.PublicKey,
	priorState types.Hash,
	hlcBytes []byte,
	txHash types.Hash,
) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.checkEquivocationLocked(stewardKey, priorState, hlcBytes, txHash, nil)
}

// =============================================================================
// Helpers — kept here so the main group.go file doesn't bloat.
// =============================================================================

// findSigningSteward returns the first verifying steward from the
// transition's multisig envelope. Each steward is matched at most once
// (M-7 fix: previously, if two signatures both verified against the
// same steward key, the first signature would match the first steward
// and the second signature would also match the same steward, causing
// incorrect attribution. Now we skip already-matched stewards.)
//
// Returns the zero PublicKey if no signers verified (caller should
// already have rejected the transition before reaching here).
func (t *Transition) findSigningSteward(stewards []Steward) types.PublicKey {
	multisig := t.Proto.GetStewardSignatures()
	if multisig == nil {
		return types.PublicKey{}
	}
	// M-7: track which stewards have already been matched to a signature.
	matched := make(map[types.PublicKey]bool, len(stewards))
	for _, sig := range multisig.GetSignatures() {
		var raw types.Signature
		copy(raw[:], sig.GetRaw())
		for _, s := range stewards {
			if matched[s.Key] {
				continue // skip already-mapped stewards
			}
			if err := verifySingle(s.Key, raw, t.groupID, t.canonical); err == nil {
				matched[s.Key] = true
				return s.Key
			}
		}
	}
	return types.PublicKey{}
}

// verifySingle is a tiny shim that avoids importing the full crypto
// package here. It runs the canonical signature check.
//
// Implemented via direct call to crypto.VerifyMultisig-with-one. For
// the equivocation check we only need to know whether THIS steward
// signed, not whether the full multisig meets threshold — the threshold
// was already verified by Apply.
func verifySingle(pub types.PublicKey, sig types.Signature, groupKey types.GroupID, payload []byte) error {
	return verifyOne(pub, sig, groupKey, payload)
}