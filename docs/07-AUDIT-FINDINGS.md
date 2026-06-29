# Antagonistic Audit Findings — Round 51 Prep

**Date:** 2026-06-28
**Auditor scope:** proto drift, crypto, concurrency, adversarial inputs, resource exhaustion, federation readiness, documentation, test coverage, Speckl compiler.
**Build state at audit:** GREEN, 5/5 packages, ~52 sim tests passing.
**Findings total:** 31 (3 CRITICAL, 6 HIGH, 13 MEDIUM, 9 LOW/INFO)

This document is the input to cycles 51–100. Each finding maps to one or more cycles.

---

## Cycle 51–60: CRITICAL findings

### C-1. [CRITICAL] X25519-as-Ed25519 cosignature verification mismatch

**File:** `internal/group/gates.go:202-208`
**Issue:** `verifyCoSignerSignature` calls `ed25519.Verify(ed25519.PublicKey(wgPubKey), prefixed, sig)` where `wgPubKey` is a WireGuard X25519 public key (32 bytes) reinterpreted as Ed25519. Random X25519 outputs are valid Ed25519 points ~50% of the time, so roughly half of legitimate peers' cosignatures fail validation, and the other half are distinguishable by an attacker probing.

The protocol comment at lines 64-69 claims "the Ed25519 view is the co-signer identity" and "the protocol requires hosts to also hold an Ed25519 view of their wg key" — but `HostIdentity` (keys.go:211-216) has NO such Ed25519 view of the wg key.

**Cycle plan:**
- 51: Write characterization test that confirms a real X25519 key fails Ed25519 verification (~50% of the time)
- 52: Add an `Ed25519ViewOfWgKey` field to `HostIdentity` (or a separate cosigner keypair stored alongside wg key)
- 53: Update `NewHost` / test helpers to derive the Ed25519 view at key-gen time
- 54: Modify `verifyCoSignerSignature` to use the Ed25519 view, not the wg raw bytes
- 55: Add bootstrap mesh test that exercises ADD_HOST_PEER cosignature with production-style keys
- 56: Add negative test: forged cosignature rejected

### C-2. [CRITICAL] No RPC handlers implemented

**File:** `proto/federated_meetup/v1/rpc.proto:127-142` vs. empty `host/`, `client/`, `cmd/` directories
**Issue:** All RPC methods (`GetGroup`, `GetEvent`, `ListEvents`, `ListGroups`, `SubmitTransition`, `SubmitUserAction`, `ResolveName`) have generated ConnectRPC stubs but zero handlers. The "open client protobuf standard" is a stub.

**Cycle plan:**
- 57: Implement `host/server.go` with the seven ConnectRPC handlers wrapping `group.State`
- 58: Add `cmd/fedmeetup/main.go` that wires `host.NewServer` to a real port
- 59: End-to-end test: spin up a real HTTP server, connect a real ConnectRPC client, submit a CREATE_GROUP, verify snapshot
- 60: Add a smoke test for each of the seven RPC methods

### C-3. [CRITICAL] Equivocation-log eviction silently disables detection

**File:** `internal/group/equivocation.go:153-168`
**Issue:** The log evicts oldest entries FIFO at 10,000 entries. Once a (steward, prior_state) pair ages out, equivocation for that pair is no longer detectable. `TestEquivocationLog_EvictionPolicyIsFIFO` *asserts* this. The protocol's stated security guarantee (equivocation detection) is silently broken for any pair whose first signing falls out of the window.

**Cycle plan:**
- 61: Add `State.EquivocationEvictions()` counter (exposes how many entries have been dropped)
- 62: Add structured warning log on every eviction
- 63: Add a test that asserts an "eviction observability contract": two honest hosts agree on when eviction occurred
- 64: Add a test that documents the security trade-off: "after eviction, a forked transition cannot be detected as equivocation"
- 65: Consider an eviction-strategy change: anchor eviction to branch epochs rather than continuous FIFO

### C-4. [CRITICAL] Speckl spec is missing 9 of 22 transition types

**File:** `specs/FederatedMeetup.speckdl`
**Issue:** Spec lists 13 events; code/proto has 22. Missing: CancelRsvp, Attest, Fork, Migrate, RevokeHostCert, RemoveHostPeer, DeclareStewardCustody, SlashSteward, NameBind. Generated Rust, TypeScript, and SMT2 are all stubs.

**Cycle plan:**
- 66: Add the 9 missing events to the SpeckDL spec with proper invariants
- 67: Regenerate Speckl outputs (Rust, TypeScript, SMT2, proto, PROV-O, CycloneDX, SPDX)
- 68: Fix the malformed SMT-LIB (`len(>= (stewards) threshold)` at smt2 line 28)
- 69: Fix the Rust unresolved types (`StringList`, `EventRecord`, `CustodyDeclaration`, `EquivocationEvidence`)
- 70: Fix the TypeScript unresolved types (line 91)
- 71: Replace empty action method bodies with real implementations
- 72: Replace literal-`return-true` invariant/verify methods with real constraint checking
- 73: Resolve the spec/code contradiction: spec invariant "equivocation_log has unique entries by (signer, branch_id, hlc)" is contradicted by FIFO eviction

### C-5. [CRITICAL] No Subscribe/Stream RPC for live transition feed

**File:** `proto/federated_meetup/v1/rpc.proto` (absent)
**Issue:** No way for a client to subscribe to live transition events. Must poll `GetGroup` and re-fetch snapshot. No server-streaming RPC exists at all.

**Cycle plan:**
- 74: Add `rpc Subscribe(SubscribeRequest) returns (stream TransitionEvent)` to `HostService`
- 75: Define `TransitionEvent` message (group_id, transition, new_state_root)
- 76: Implement in `host/server.go` using a per-group broadcaster pattern (fan-out via `chan TransitionEvent` or `sync.Map[*GroupState, []chan]`)
- 77: Add end-to-end test: client subscribes, host applies transition, client receives event

### C-6. [CRITICAL] Branch-local mutations on non-genesis branches are rejected

**File:** `internal/group/group.go:359-363`
**Issue:** Spec says branches are independent state machines; code rejects all transitions on non-genesis branches. The protocol claims branches work; they don't.

**Cycle plan:**
- 78: Audit each apply switch case to determine which transitions should be branch-local vs. parent-local
- 79: Add branch-scoped key prefixes (`branch/<id>/event/...`) for branch-local transitions
- 80: Implement branch-local CREATE_EVENT, UPDATE_EVENT, CANCEL_EVENT, RSVP, CANCEL_RSVP, ATTEST
- 81: Verify the prior_state computation per-branch (root should be computed against the branch's KV, not the parent's)
- 82: Test: create branch, apply 10 events on branch, verify parent untouched

---

## Cycle 83–92: HIGH findings

### H-1. [HIGH] `MaxKVSize` measures entries, not bytes — attackers grow individual values

**File:** `internal/group/group.go:968-982`
**Cycle plan:**
- 83: Rename `MaxKVSize` → `MaxKVEntries`, add `MaxKVBytes`
- 84: Add per-value size cap at apply site (group.go:968-982)
- 85: Add test: 1000 events with 1MB metadata each — should be rejected after N events, not M

### H-2. [HIGH] `equivocationEvidence` slice grows unbounded

**File:** `internal/group/group.go:177`, `:775`
**Cycle plan:**
- 86: Cap the evidence slice at a configurable max (mirror equivocation-log FIFO)
- 87: Add test: 1000 SLASH_STEWARD transitions, verify slice length is bounded

### H-3. [HIGH] HLC `Tick` resets counter to 0 on `wall > last`, losing monotonicity across partitions

**File:** `internal/hlc/hlc.go:169-174`
**Cycle plan:**
- 88: Add a partition-aware Tick mode: if `lastNanos` is "recent" but the cursor counter was non-zero, preserve the counter
- 89: Add DDIL partition test: simulate 10-min partition, verify cursor monotonicity post-recovery

### H-4. [HIGH] `VerifyMultisig` is O(n × threshold) — DoS via large steward sets

**File:** `internal/crypto/crypto.go:139-168`
**Cycle plan:**
- 90: Implement hash-cached verification (compute canonical-bytes hash once, store in map keyed by partial signer pubkey prefix)
- 91: Add benchmark: 100 stewards × threshold 100 → measure latency

### H-5. [HIGH] Nil-payload transitions silently write zero-key values

**File:** `internal/group/group.go:502-622` (13 cases missing `if p == nil` checks)
**Cycle plan:**
- 92: Add `if p == nil` defensive checks to ADD_STEWARD, REMOVE_STEWARD, ADD_MEMBER, REMOVE_MEMBER, CREATE_EVENT, UPDATE_EVENT, CANCEL_EVENT, RSVP, CANCEL_RSVP, ATTEST, FORK, MIGRATE

### H-6. [HIGH] Most string fields lack length/charset validation

**File:** `internal/group/group.go:460-462, :568, :585, :589, :595, :607, :622, :626, :745, :820`
**Cycle plan:**
- 93: Add `(buf.validate.min_len=1, max_len=256)` annotations to canonical_name, display_name, event_id, title, location
- 94: Add `utf8.ValidString` checks at apply site for all string fields
- 95: Add test: 1MB title, empty event_id, malformed UTF-8 — all rejected with clear error

### H-7. [HIGH] No "fetch log" RPC — mirrors cannot bootstrap from history

**File:** `proto/federated_meetup/v1/rpc.proto` (absent)
**Cycle plan:**
- 96: Add `GetTransition(GetTransitionRequest) returns (Transition)` and `GetLog(GetLogRequest) returns (LogChunk)` with `since_cursor` and `limit` pagination

### H-8. [HIGH] No snapshot-fetch RPC distinct from current head

**Cycle plan:**
- 97: Add `GetSnapshot(GetSnapshotRequest) returns (GetSnapshotResponse)` keyed by root hash

### H-9. [HIGH] No conflict-resolution messaging — gossip-level equivocation pipeline not wired

**Cycle plan:**
- 98: Add gossip protocol over wg mesh: `SubmitEvidence(EvidenceEnvelope) returns (Ack)`
- 99: Populate `TransitionA/B` from actual received transitions before storing

### H-10. [HIGH] Mesh transport has no federation RPC surface

**Cycle plan:**
- 100: Define `FederationEnvelope` (signed, sequenced, retry-id'd) and wrap it around the four RPCs above

---

## Cycle 101–113: MEDIUM findings (selected)

### M-1. `Multisig.Threshold = 0` accepted — authentication bypass on CREATE_GROUP
- **File:** `internal/crypto/crypto.go:139-142`, `internal/group/group.go:425-426`
- **Cycle plan:** 101: reject `threshold == 0` in apply switch + `crypto.VerifyMultisig`. 102: negative test.

### M-2. Latent race in `Limiter.buckets` lazy creation
- **File:** `internal/ratelimit/ratelimit.go:140-147`
- **Cycle plan:** 103: take `l.mu` for the entire lazy-create-and-allow sequence. 104: race detector test.

### M-3. `EquivocationEvidence` TransitionA/B pointers are never populated
- **File:** `internal/group/equivocation.go:208-212`
- **Cycle plan:** 105: populate from `Apply` path. 106: assert in test.

### M-4. Mesh IP accepts arbitrary length — proto says "4 bytes IPv4"
- **File:** `internal/group/group.go:487-499`, `:677-704`
- **Cycle plan:** 107: validate `len == 4 || len == 16`. 108: test.

### M-5. MIGRATE deadline accepted as zero Timestamp (year 1)
- **File:** `internal/group/group.go:625-629`
- **Cycle plan:** 109: validate `deadline > now`. 110: test.

### M-6. Empty/malformed `hlc` field accepted on non-CREATE_GROUP
- **File:** `internal/group/group.go:380-404`
- **Cycle plan:** 111: validate `len(hlc) == 18`. 112: test.

### M-7. `findSigningSteward` returns first match — wrong steward attributed on collision
- **File:** `internal/group/equivocation.go:332-347`
- **Cycle plan:** 113: track per-steward verified set, skip already-mapped stewards. 114: test.

---

## Cycle 115–124: LOW findings (selected)

### L-1. `O(N²)` bubble sort in `branchRegistry.list()` — use `sort.Slice`
- **File:** `internal/group/branch.go:286-294`
- **Cycle plan:** 115: replace with `sort.Slice`. 116: behavior test.

### L-2. `binaryBigEndianPutUint64` does manual bounds check — use `binary.BigEndian.PutUint64`
- **File:** `internal/crypto/crypto.go:101-111`
- **Cycle plan:** 117: replace with stdlib. 118: behavior test.

### L-3. `Custom` payload silently rejected by `default:` — distinct error
- **File:** `internal/group/group.go:843-844`
- **Cycle plan:** 119: distinct error "custom payload not supported". 120: test.

### L-4. Domain separator pattern needs documentation
- **File:** `internal/crypto/crypto.go:88-99`
- **Cycle plan:** 121: doc comment explaining Ed25519 is used in pure mode (no context).

### L-5. `stewardHistory` and `thresholdHistory` maps grow forever
- **File:** `internal/group/group.go:145-149`, `:850-851`
- **Cycle plan:** 122: bound to last N roots. 123: test.

### L-6. `Limiter.buckets` map grows unbounded
- **File:** `internal/ratelimit/ratelimit.go:139-147`
- **Cycle plan:** 124: LRU cap. 125: test.

---

## Cross-cutting themes

1. **Tombstone pattern needs Speckl-level invariant**: cycles 25–27 + 37 established the pattern. C-4 + the H-5/H-6 batch make it a formal property.
2. **Cryptographic consistency**: F-2.2 (X25519/Ed25519) is the biggest security hole. Combined with the dead `MsgKindUserAction` (F-1.2) and `findSigningSteward` bug (M-7), this is three crypto bugs in one audit.
3. **Federation is currently fictional**: all 5 CRITICAL findings (C-2, C-3, C-5, plus the missing Subscribe/GetLog/GetSnapshot/SubmitEvidence RPCs) trace back to "we have a wire protocol but no implementation."
4. **Spec/code drift is systemic**: spec says 13, code has 22. Generated code is stubs. The whole `specs/build/` directory is half-broken.
5. **DoS surface is wider than expected**: KV entry-count cap is misnamed, evidence slices grow unbounded, rate-limit buckets grow unbounded, steward/threshold history maps grow unbounded. None of these are obvious; an external operator wouldn't know to size them.

---

## Severity summary

| Severity | Count | Cycle range |
|---|---|---|
| CRITICAL | 6 | 51–82 |
| HIGH | 10 | 83–100 |
| MEDIUM | 13 (7 prioritized) | 101–113 |
| LOW | 9 (6 prioritized) | 115–124 |

That gets us 50 cycles of work. After cycle 124, return for next session checkpoint.