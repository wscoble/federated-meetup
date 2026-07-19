# 02 — The Protocol

**Scope:** This document defines the open protocol that any host can implement to participate in the federation. The protocol is free as in speech. It does not encode commercial policy. It does not encode UX decisions. It does not encode the tariff. It is the substrate on which commercial products are built.

**Out of scope:** The host product, the tariff, the UX, the event-operations feature set, the reputation-aggregation services, the discovery layer, the customer support model, the growth strategy. Those are in `03-PRODUCT.md` and `04-TARIFF.md`. If a question is "what does the *host* do?", it does not belong in this document.

---

## 1. The model in one paragraph

A **group** is a sovereign object identified by a cryptographic keypair. The group's *state* (member list, event history, steward set, fork history, signed attestations) is a replicated state machine, addressable by the group's public key. Any number of **hosts** can serve a group by storing a copy of the state and providing a UI on top. A **user** has a cryptographic identity, lives on at least one host, and can interact with any group in the federation regardless of which host serves the group or which host serves the user. **Stewards** are the humans who control the group's keypair. **Forks** are new sovereign groups whose state diverges from a parent group at a chosen snapshot. **Mirrors** are read-only (or eventually-consistent write) copies of a group's state hosted by a different host for performance and redundancy.

## 2. Identities

### 2.1 Group identity

Each group has an Ed25519 keypair. The public key is the group's canonical identifier on the network. The private key is held by the group's stewards, distributed across them with a threshold signature scheme (e.g., 2-of-3, 3-of-5, 5-of-7 — the group's stewards choose at creation time, the protocol does not pick).

The private key is the source of truth for *who speaks for the group*. If 2-of-3 stewards must sign a state transition, the protocol enforces that. The protocol does not care *which* 2-of-3 — that is the group's policy.

The keypair is created at group creation. The private key is the most security-critical object in the system. The protocol specifies a wallet-style key-custody model (cold storage for primary key, hot steward signatures for routine operations) but does not require a specific implementation.

### 2.2 User identity

Each user has an Ed25519 keypair. The public key is the user's canonical identifier. Users can hold multiple identities (work, personal, anonymous for sensitive meetups) — the protocol does not require a single identity per human.

A user's identity is portable across hosts. If a user is on Host A and wants to attend an event on a group served by Host B, the user does not need an account on Host B. The user signs the RSVP with their private key, the RSVP travels with the signature, and Host B can verify the signature against the user's public key (which is in the public key directory).

### 2.3 Steward identity

A steward is a user identity that has been added to a group's steward set. The group's state machine records steward additions, removals, and threshold changes as signed transitions.

The protocol does not distinguish "steward" from "user" at the cryptographic level. A steward is a user identity with a signed role-attestation from the group.

## 3. State machine

The group's state is a **forest of branches**, each branch an independent state machine. A branch carries its own Merkle KV state, transition log, equivocation log, steward history, and threshold history. Branches share the group's keypair (so cross-branch messages verify) and the cross-cutting registry (mesh peers, custody declarations, equivocation evidence list). They do NOT share state mutations.

This is the load-bearing 2026-06-27 architectural shift. It exists because:

- **Mutation must be local to a branch.** Two stewards who disagree on the group's direction should not have to fight over the same log; they each create a branch and the community arbitrates by showing up.
- **Fork-cost must be cheap.** Branching is the cheap fork — it inherits the group's keypair, steward set, and threshold. The expensive sovereign split (a new group with its own keypair and stewards) is FORK, distinct from BRANCH_CREATE.
- **No fork-rate gate.** Branching is supposed to be easy. Capping the rate of branching recreates the lock-in problem the branching primitive exists to solve. The cap is on per-group BRANCH count (MaxBranches), not on creation rate.

### 3.0 Branches

- Every transition targets exactly one branch, identified by `Transition.branch_id` (uint32, default 0).
- Branch 0 is the **genesis branch**, created by CREATE_GROUP. It holds the initial steward set and threshold.
- BRANCH_CREATE allocates the next monotonic branch ID (1, 2, 3, ...) and copies the parent's stewards + threshold at the snapshot. The new branch inherits the group's keypair.
- Branch IDs are never reused within a group's lifetime.
- A branch's KV state, transition log, and equivocation log are independent of every other branch.
- Cross-branch comparisons are meaningless — branches are different state machines that share only the group's keypair.
- Cross-branch "equivocation" is not detected (different state machines, different semantics).
- A host serves one or more branches; clients pick which branch to follow via BRANCH_ADOPT (a local, host-side operation, not a transition).

### 3.0.1 The two kinds of split

| Operation | Use case | What's shared | What's new |
|---|---|---|---|
| **BRANCH_CREATE** | "I disagree with the direction but I still want to be part of this group" | Group keypair, steward set, threshold, group identity | A new branch ID + an isolated state machine |
| **FORK** | "This group is captured / hostile / dead — I'm taking the history and leaving" | Nothing — full sovereign split | New keypair, new stewards, new threshold, new group identity |

FORK is a special case of BRANCH_CREATE that copies the parent's state into a new group (not just a new branch). Use BRANCH_CREATE alone for cheap disagreement; use FORK for a full sovereign split.

### 3.1 Per-transition canonical-form

Every transition has:

The protocol defines a small set of canonical transition types. Hosts and clients may add custom types as opaque payloads, but the canonical types are what make two hosts interoperable.

- `CREATE_GROUP` — initial state, sets the steward set, threshold, group name, and metadata
- `ADD_STEWARD` — adds a user identity to the steward set
- `REMOVE_STEWARD` — removes a user identity from the steward set
- `CHANGE_THRESHOLD` — changes the steward signing threshold
- `ADD_MEMBER` — adds a user to the group's member list
- `REMOVE_MEMBER` — removes a user from the group's member list
- `CREATE_EVENT` — creates a new event (title, time, location, capacity, free/paid, ticket info)
- `UPDATE_EVENT` — modifies event fields
- `CANCEL_EVENT` — marks an event as cancelled
- `RSVP` — a user signing that they will attend an event
- `CANCEL_RSVP` — a user retracting an RSVP
- `ATTEST` — a signed attestation from one identity to another (used for reviews, endorsements, and the reputation layer)
- `BRANCH_CREATE` — creates a new branch within the same group (cheap disagreement; see §3.0.1)
- `FORK` — creates a new sovereign group whose state diverges from this group at a given snapshot (full split; see §3.0.1)
- `MIGRATE` — moves a group's hosting from one host to another (signed by the steward threshold)

Hosts and clients may add custom transition types for product features, but those types do not need to be implemented by every host to maintain interop — they will be ignored or shown as opaque data by hosts that do not implement them.

### 3.2 What the protocol does NOT define

- The format of event locations (freeform string vs. structured address vs. venue object)
- The format of ticketing, prices, payment rails
- The format of reputation aggregation
- The format of group descriptions, member profiles, photo storage
- Any UX decision

These are product concerns. Hosts and clients may define their own formats and exchange them as opaque blobs within the state. Cross-host compatibility is a goal but not a protocol requirement.

### 3.3 Canonical event identifiers

An event is uniquely identified by the tuple `(group_key, branch_id, event_id)`. The dedup key across hosts is the same tuple — two hosts serving the same group and exposing feeds (RSS/Atom/JSON) will emit the same event under the same key, so a crawler or AI agent that sees the event from both hosts can deduplicate on `(group_key, branch_id, event_id)` without further coordination.

A recurring event (a series with multiple occurrences sharing the same `event_id`) adds `occurrence_unix` to the tuple, giving `(group_key, branch_id, event_id, occurrence_unix)`. `occurrence_unix` is the Unix timestamp of the specific occurrence; it is set by the host that first observed the event and is stable across propagation.

The `(group_key, branch_id, event_id, occurrence_unix)` tuple is signed by the host that first observed the event (the host whose steward threshold signed the `CREATE_EVENT` / `UPDATE_EVENT` transition) and propagated to other hosts in the transition stream. Duplicate events arriving from other hosts — same tuple, different source — are dropped by the receiver. This closes the dedup gap noted in `06-OPEN-QUESTIONS.md` §17: the dedup key is canonical and protocol-defined, not a client-side heuristic.

## 4. Forks

A fork creates a new group whose initial state is a snapshot of the parent group's state at a chosen block-height. The fork is its own sovereign group, with its own keypair, its own stewards, its own state machine. The fork and the parent are siblings, not parent-child. The protocol records the fork lineage, but lineage does not confer authority.

A fork is signed by a single steward of the parent group. The protocol's job is to make the fork easy. The group's policy may impose a higher threshold (e.g., "forks require 2-of-3 steward signatures") by encoding that as a state-transition rule, but the protocol itself does not require it.

This is the design choice that resolves the "dead owner" / "incapable owner" / "stolen group" problem. A steward who disagrees with the group's direction can fork. The original group keeps going. The fork is a new group. The community arbitrates by showing up. The protocol does not pick winners.

## 5. Hosts

A host is a service that stores a group's state and serves it to clients. The protocol does not require any specific host architecture. A host may be a single server, a replicated database, a CDN, a peer-to-peer node, or a self-hosted Raspberry Pi.

### 5.0 Two transports, not one

The federation has **two distinct transport surfaces**. This is load-bearing. Conflating them collapses the security model.

**Client → Host: open protobuf over ConnectRPC.** Clients (web apps, mobile apps, scripts) speak ConnectRPC against the host's public HTTP/2 endpoint. The wire format is the open protobuf standard in `proto/federated_meetup/v1/`. Anyone can implement a client; we ship a Go reference client as open source. Hosts expose this surface; the surface is reachable from the public internet (over TLS).

**Host → Host: private WireGuard mesh.** Federation traffic between hosts flows over a userspace WireGuard overlay. The overlay IP space is private to the federation; nothing about the federation service is addressable from the public internet. This is what makes the federation actually federated: the host-to-host surface has no public attack surface.

> **Clarification on the public surface.** `08-DISCOVERY.md` §3.1 exposes `federation_peers` (a list of peer *client* URLs, e.g. `https://phoenixdevs.org`) on the public `/.well-known/federation` endpoint. These are Layer 3 client-facing URLs used for bootstrapping peer discovery by crawlers and AI agents — they tell a client "here are other hosts you can talk to as a client." The federation mesh itself (Layer 1, the WireGuard overlay, host-to-host transition replication) remains private. Exposing a peer's client URL is not the same as exposing the mesh.

The two transports speak different protocols over different layers:

| Surface | Wire | Encryption | Reachable from |
| --- | --- | --- | --- |
| Client → Host | ConnectRPC over HTTP/2 (TLS) | TLS | Public internet |
| Host → Host | Custom protocol over WireGuard | WireGuard (Noise IK) | Private mesh only |

Why two and not one? Because if server-to-server traffic rode the same RPC surface as clients, every host would have a public attack surface equal to its federation surface. By keeping server-to-server traffic on a private overlay, the federation traffic is private by construction — no rate-limiting middleware, no authn at the federation boundary, just cryptographic identity verified by the steward set's threshold signature.

**Operational implication for hosts.** A host runs two listeners:
1. A public ConnectRPC server (HTTPS) for clients.
2. A WireGuard interface participating in the federation mesh, with a static private IP.

The mesh does not require any specific network topology. Hosts can be behind NAT. The mesh uses userspace WireGuard (golang.zx2c4.com/wireguard) so it runs without root and is portable across Linux/macOS/Windows/embedded targets.

**Operational implication for the simulator.** The federation has to survive Vegas-to-Phoenix flakiness. The simulator models DDIL (Denied/Disrupted/Intermittent/Limited) conditions on the mesh as a first-class concern. See `sim/ddil.go` for the profiles. This is non-negotiable — a federation that only works on a healthy datacenter LAN is not a federation.

### 5.1 Three crypto layers, three independent key domains

The federation has **three cryptographic layers** with **three independent key domains**. Each layer answers a different question and uses keys that are not derivable from one another. Compromise of any single layer does not imply compromise of any other layer.

| # | Layer | Question it answers | Key domain | Wire |
|---|---|---|---|---|
| 1 | **WireGuard mesh** | "Is this host one of the peers I trust enough to receive packets from?" | X25519 per host (wireguard) | UDP, host↔host, mesh-private |
| 2 | **Multisig governance envelope** | "Did M-of-N stewards authorize this state transition, with fresh intent, in a verifiable chain?" | Ed25519 per steward | Inside the wg tunnel |
| 3 | **Client ConnectRPC + TLS** | "Is this client authorized to perform this action against this host?" | Server TLS cert + (TBD) client auth | HTTPS, public internet |

**Why three and not one?** Because each layer has a different threat model and a different compromise profile:

- **Layer 1 compromise (stolen wg key)** lets an attacker impersonate a host on the mesh for the lifetime of the handshake. It does NOT let them sign transitions, because Layer 2 is a separate Ed25519 key.
- **Layer 2 compromise (stolen steward key)** lets an attacker sign transitions as that steward. It does NOT let them impersonate a host on the mesh, because Layer 1 is a separate X25519 key. Multisig threshold (M-of-N, M > 1) means a single stolen steward key cannot unilaterally move state.
- **Layer 3 compromise (stolen TLS key)** lets an attacker impersonate the host to clients. It does NOT affect federation integrity, because the mesh is private and unrelated.

**Keys never derive from one another.** A host's wg private key, a host's steward private key, and a host's TLS private key are three independent random keypairs. They are not derived from each other, not HKDF-derivative, not cross-signed. The only thing that crosses between layers is **messages**: a transition signed with a Layer 2 key travels through a Layer 1 tunnel and reaches a Layer 3 client. The keys themselves stay in their domains.

**Key rotation is independent.** A host rotates its wg key without rotating its steward key. A steward rotates their Ed25519 key by sending an `ADD_STEWARD` + `REMOVE_STEWARD` transition pair (a Layer 2 operation); it has zero effect on Layer 1 or Layer 3.

**Implementation rule.** No code path may accept a key from one layer as input to another. `internal/crypto/` exposes three types (`WireGuardKey`, `StewardKey`, `TLSKey`) that wrap their respective key material and refuse to interconvert at the type level. A function that needs a `StewardKey` cannot be called with a `WireGuardKey` even though both are 32 bytes; the compiler enforces this.

### 5.1.1 Clock-independent ordering — Hybrid Logical Clocks (HLC)

Federation hosts will have wall-clocks that drift, jump (NTP step), and suspend/resume. A Las Vegas host's clock and a Phoenix host's clock may differ by seconds, minutes, or hours depending on how long each has been up and what NTP has done. We **cannot rely on wall-clock** for total ordering of transitions across the federation.

Layer 2 (multisig envelope) therefore carries an **HLC** — Hybrid Logical Clock — alongside the existing sequence number. The HLC is the authoritative ordering primitive. The wall-clock `signed_at` field on the transition remains for audit/UX but is **advisory only** and not used for ordering.

**HLC format.** 18 bytes on the wire (matches `Transition.hlc` field):

```
| 8 bytes wall nanos BE | 2 bytes counter BE | 8 bytes reserved |
```

The wall component is the host's best estimate of "when" the transition was authored, in nanoseconds since the Unix epoch. The counter is a per-host logical clock that breaks ties when two transitions share a wall component.

**Total order across hosts.** HLCs compare by their byte representation (big-endian). The wall component dominates, then the counter. Two transitions from different hosts can have **equal** HLCs — that is permitted and does not violate the protocol. What HLC guarantees is:

1. **Per-host monotonicity.** A host's HLC strictly increases across transitions it authors, even when its wall-clock goes backwards (NTP step, suspend/resume). The counter component advances to preserve order.
2. **Causality.** If host A observes a message from host B (i.e., A applies B's transition), then A's next locally-generated HLC is strictly greater than B's.
3. **Bounded wall drift.** The wall component of any host's HLC is bounded by `max(local_clock, remote_HLC_wall, observed_remote_wall) + drift`. In practice, "approximately when" is accurate to within a few seconds of real wall-clock.

**Why not Lamport clocks?** Pure Lamport (counter only) loses the wall-clock-shaped component, which humans want in audit logs. We pay the 8-byte cost to keep "approximately when" alive while still being totally ordered.

**Why not vector clocks?** Vector clocks track per-host causality. Our multisig envelope already gives fine-grained causality via the hash chain and the prior_state reference. Adding a vector clock would add O(N) bytes per transition for information we already have. HLC is cheaper and sufficient.

**Why not wall-clock only?** Because federation hosts have unsynchronized clocks. Two hosts that each think they signed a transition at 12:00:00.000 have no shared ground truth. HLC gives them a total order without requiring sync.

**Failure modes.** HLC degrades gracefully:

- *Clock skew* (constant offset, e.g. one host's clock is an hour behind): cursors still merge; each host's authored HLC reflects its own skewed wall but ordering is total.
- *Clock step backwards* (NTP step, suspend/resume): counter advances; wall sticks with the last-seen value.
- *Clock step forwards* (large jump): counter resets; wall moves forward cleanly.
- *Partition* (host isolated for an hour): on rejoin, host Observe()s everything in the partition log; its cursor jumps past all of them.

**Reference.** Kulkarni et al. (2014), "Logical Physical Clocks and their Applications in Distributed Systems." Implementation in `internal/hlc/`. Drift bound is the host's concern; this package makes no assumption about it.

### 5.2 What a host must do

- Accept and verify signed state transitions from a group's stewards
- Persist the state in a way that other hosts can replicate
- Serve the state to clients
- Reject state transitions that are not properly signed or that conflict with prior state
- Provide a discovery endpoint so clients can find the group (see section 7)

### 5.3 What a host may do

- Impose commercial terms (subscription, fees, tariffs)
- Add value-added services (ticketing, payments, analytics, recommendations)
- Curate groups (refuse to host certain groups)
- Brand the experience (own logo, own colors, own UX)
- Add or hide product features

### 5.4 Threat model and hardening

Federation hosts run in adversarial environments — untrusted VPSes, contested networks, hostile peers. The protocol defends against specific attack classes with concrete primitives. Each primitive has a regression test in the simulator; the test ID and assertion are listed below.

#### 5.4.1 Equivocation detection

**Attack.** A malicious steward (or one whose key has been compromised) signs two different transitions at the same `prior_state`. The first applies; the second would fail prior-state check, but by then the malicious host may have gossiped both to different parts of the federation, splitting honest hosts' views of the state. This is a fork-by-insider attack.

**Defense.** Every transition's signing steward is recorded in an **equivocation log** keyed by `(steward_pubkey, prior_state)`. A second distinct signed transition at the same key triggers `EquivocationEvidence`. Honest hosts that observe the evidence gossip it to peers and sign a `REMOVE_STEWARD` transition against the offending key. The multisig threshold means a single equivocating key cannot unilaterally move state — but it CAN split views across the network if not detected quickly.

**Where.** `internal/group/equivocation.go`. Public test surface: `(*group.State).CheckEquivocation(steward, prior, hlc, txhash)`.

**Test.** `sim/threat_test.go::TestThreat_EquivocationRejected` — pre-seeds the log with one signed transition, asserts that a second distinct `(steward, prior, hlc, txhash)` tuple is flagged, and that a replay (same tuple) is not flagged.

**Status.** v1: data-structure detection only. End-to-end gossip and slash-on-evidence is an open question (see §11).

#### 5.4.2 HLC drift validation

**Attack.** A malicious peer injects messages with HLC wall components far in the future (e.g. `wall = now + 1000 years`). If a host blindly `Observe`s these, its cursor jumps forward. Subsequent legitimate messages appear "old" by comparison, and the legitimate messages' `(wall, counter)` ordering gets compromised — an attacker can effectively "use up" future ordering space.

**Defense.** `sim.Host.Deliver` rejects any inbound message whose HLC wall is more than `MaxHLCDrift` (default 60s) ahead of the host's local clock. The rejection increments the host's `DroppedMessages` counter and returns `ErrHLCDriftExceeded`. The legitimate cursor is never advanced by the malicious message.

**Configuration.** `sim.MaxHLCDrift` package-level default; `(*Host).SetMaxHLCDrift(d)` per-host override. Production hosts should set drift to the maximum expected NTP-skew in the federation (default 60s is generous).

**Tests.** `TestThreat_HLCDriftRejected` (1-year-future HLC → rejected), `TestThreat_HLCDriftAtBoundary` (5s drift limit → +4s accepted, +6s rejected). Both verify the dropped counter increments.

**Trade-off.** Tighter drift bound = less tolerance for legitimate skew. Looser bound = more attack surface. The right value depends on the federation's actual clock quality. Hosts SHOULD log drift rejections so operators can tune.

#### 5.4.3 Steward set bound

**Attack.** A malicious steward repeatedly calls `ADD_STEWARD` to inflate the steward set. Two damage paths: (1) state bloat → OOM; (2) multisig verification is O(N) per transition → CPU exhaustion as N grows.

**Defense.** `group.State.MaxStewards` defaults to 100. `ADD_STEWARD` is rejected once the prospective steward set would exceed this cap. The check uses the **prospective** count (current + new key, deduped), not the stale pre-transition count.

**Configuration.** `(*group.State).MaxStewards = N`. Production deployments SHOULD keep this low (the protocol's threshold sig migration to FROST eliminates the O(N) verify, but the bound remains as defense in depth).

**Test.** `TestThreat_StewardSetBound` — caps at 5 stewards, attempts 3 adds; first 2 succeed (3 → 4 → 5), 3rd is rejected with an explicit error message.

#### 5.4.5 Per-steward transition rate limit

**Attack.** An adversary who controls one steward key floods `SubmitTransition` calls. Each transition is signed (cost: CPU on authoring host) and broadcast (cost: mesh bandwidth on every peer). Even though signature verification is the expensive part on receiving hosts, the FLOOD of inbound messages overwhelms CPU and bandwidth before individual messages can be rejected for content.

**Defense.** Token-bucket rate limiter per `(steward_pubkey, group_id)`. Each transition consumes one token from the signing steward's bucket for the target group. The bucket refills at a configurable rate up to a configurable burst. Buckets are lazily created on first use.

The check fires in `group.State.Apply` BEFORE the equivocation check (so rate-limited attempts don't pollute the equivocation log) and BEFORE signature verification (so the cheap rejection happens first). The first verifying signer in the multisig envelope is the bucket owner — the assumption is that the author of the message is responsible, even if co-signers are honest.

**Where.** `internal/ratelimit/` (`Bucket`, `Limiter`). Wired into `group.State.Limiter` field; nil by default (opt-in). `(*group.State).Limiter = ratelimit.NewLimiter(rate, burst, clock)`.

**Defaults.** 10 transitions per second per steward per group, burst 10. Tune per deployment — higher for active event-creation hosts, lower for read-mostly mirrors.

**Tests.** `internal/ratelimit/ratelimit_test.go` (unit): burst, refill, partial refill, per-key quota, lazy bucket creation, nil-clock fallback. `sim/threat_test.go::TestThreat_TransitionFloodingRejected` (integration): burst 3, fourth call rejected, refill over virtual clock allows next call.

**Trade-offs.**
- **Clock source.** Production uses `time.Now`. The simulator passes a virtual clock so the test is deterministic. Production hosts SHOULD log rate-limit rejections and tune the bucket size based on observed rejection rates.
- **Bucket size.** Too low = legitimate stewards (e.g. a host pushing a 1000-RSVP event-creation batch) get throttled. Too high = the defense is ineffective. The right value is workload-dependent.
- **Per-group vs global.** v1 is per-group, so a steward authoring for many groups gets N× the throughput. v2 may want a global cap per steward key.

#### 5.4.6 What these defenses DON'T cover (yet)

The hardening above covers the four highest-likelihood attacks under the threat model Scott enumerated. The following are **not yet implemented** and are tracked in §11:

- **Steward set growth via REMOVE_STEWARD bypass** — a steward could rotate keys to grow effective N. Not yet modeled.
- **Merkle state root collision** — SHA-256 is currently secure. Quantum-computing forward-compat via SHA-3-256 swap-in is documented in §11 but not yet specced.
- **Fork/migrate races** — split-brain via two concurrent MIGRATE transitions from different hosts not yet handled.
- **Compromised wg host** — passive observation defense is via §5.1 key separation; active defense (slash-via-attest) is in the open questions list.
- **Gossip-level equivocation pipeline** — the equivocation log detects the data structure but the end-to-end "gossip evidence + slash via REMOVE_STEWARD" is not yet wired.

#### 5.4.7 Branch-local mutation

**Attack.** A malicious steward or compromised host floods the federation with high-volume state mutations on a single group, hoping to overwhelm signature verification, KV storage, or operator attention. Earlier design (pre-2026-06-27) considered a fork-rate gate; that approach was rejected because cap-on-rate punishes legitimate disagreement and recreates lock-in.

**Defense.** Mutations are local to a branch. Each branch is an independent state machine with its own Merkle KV, transition log, equivocation log, steward history, and threshold. A flood on branch N has zero effect on branch M — not even equivocation detection crosses branches, because branches are different state machines. The cap on per-group branches (`MaxBranches`, default 1000) bounds total branches, not creation rate.

**Test.** `internal/group/branch_test.go::TestBranch_BranchCapEnforced` — sets `MaxBranches=2`, creates branches 0 and 1, rejects the third creation. `TestBranch_TransitionMustReferenceExistingBranch` — verifies that transitions targeting non-existent branches are rejected (a host can't pretend to mutate a branch it hasn't seen).

**Trade-off.** Branch-locality means a fork-as-sovereign-split (FORK) and a fork-as-disagreement (BRANCH_CREATE) live at different protocol layers. Hosts serving many branches pay storage cost per branch; production hosts SHOULD prune branches with no recent activity (out of scope for v1 — tracked in §11).

### 5.5 What a host must not do

- Modify the group's state without a valid signed transition
- Hold a group's private key (the key is held by the stewards, not the host)
- Refuse to let a group migrate to a different host (a group can always sign a `MIGRATE` transition and leave)
- Discriminate between groups in ways that violate the host's published policy

The protocol enforces 5.3 at the cryptographic level (the steward key controls the state), not at the social level (the host can still de-platform, the protocol cannot stop that). The mitigation for de-platforming is mirrors: a group mirrored across multiple hosts cannot be killed by any single host.

## 6. Users

A user signs state transitions with their private key. A user can interact with any group in the federation. A user does not need an account on every host — they need an account on at least one host, and they need their private key.

### 6.1 What the protocol does for users

- Verifies signatures
- Resolves user identities across hosts
- Resolves groups across hosts
- Routes RSVPs, attestations, and other signed messages to the right group and host

### 6.2 What the protocol does not do for users

- Recover lost keys
- Moderate abusive users
- Provide customer support
- Provide discovery or recommendations

## 7. Discovery

Discovery is the part of the system that lets a user find a group, given a human-readable name. The protocol defines a thin layer:

- Every group has at least one **canonical name** (a string, like `vegas-programmers` or `lv-wordpress`)
- A name is resolved to a group identifier through a **public directory**
- A public directory is a service that maps names to group identifiers (and the current host serving them)
- A public directory is itself a service that any party can run
- A public directory is a host (it speaks the protocol) and can be queried by clients

The protocol does not require a single canonical directory. There can be many. A client picks one (or runs its own). A name registered in one directory is not automatically registered in another — but directories can sync via a published protocol (a draft is in section 11).

This is the model that makes the protocol "free as in speech" at the discovery layer. No one party controls the name space. Different directories can have different policies (e.g., a directory that requires verification for a `vegas-programmers` name, vs. an open directory that lets anyone claim any name).

> **Scope note.** The protocol's directory is name→key resolution. Richer directory services (search, recommendations, semantic/embedding-based discovery, geographic filtering) are a host/product concern and out of protocol scope per §6.2. The directory extensions documented in `08-DISCOVERY.md` §3.5 are host-layer services, not protocol requirements.

## 8. The reputation layer

The protocol supports a reputation layer through the `ATTEST` transition. An attestation is a signed message from one user identity to another, with a schema and a payload.

The protocol does not aggregate attestations. The protocol does not produce reputation scores. The protocol does not decide what makes a good attestation. The protocol just makes attestations portable.

Reputation aggregation is a product-layer service. There can be many aggregators. A user picks which aggregator to trust. Aggregators can compete on the quality of their models. Aggregators can be public-good, commercial, or community-run.

The protocol's only job in the reputation layer is to ensure that an attestation follows the attested identity across hosts. A user on Host A who has been attested by 50 organizers on Host B is verifiable on Host A. That is the structural property that makes reputation portable.

## 9. Mirrors

A mirror is a host that stores a read-only (or eventually-consistent write) copy of a group's state. Mirrors serve two purposes: performance (a user in Europe can read a US-hosted group from a European mirror) and resilience (if the primary host goes down, the mirror still serves the group's read state).

The protocol defines a replication format: a stream of state transitions, signed, that a mirror can verify and replay. The format is content-addressed (transitions are identified by their hash) so that mirrors can deduplicate and verify.

Writes still go to the primary host (the host whose steward threshold is signing the transitions). Mirrors either accept read-only clients or buffer writes for re-submission to the primary.

The protocol does not require a specific mirror architecture. A mirror may be a CDN, a peer-to-peer node, a backup service, or a self-hosted server.

## 10. Migration

A group can migrate from one host to another. Migration is a `MIGRATE` transition, signed by the steward threshold, that names the new host and a deadline after which the old host is no longer the canonical host for the group.

The protocol's job is to make migration atomic and verifiable. Both the old host and the new host can serve the group during the migration window. After the deadline, the new host is canonical. The old host can choose to keep serving the group as a mirror.

Migration is the property that prevents host lock-in. A group can always leave. This is the property that makes the protocol free as in speech in a stronger sense than the AGPL-style "you can fork the code" freedom — a group can leave a host without forking the code or forking the group.

## 11. Open protocol questions

These are not in the spec yet:

- The exact format of the public directory sync protocol
- The exact threshold signature scheme (FROST, Ed25519 threshold, or other). The v1 implementation uses a multisig envelope (each steward signs with their own Ed25519 key; the host verifies that at least `threshold` distinct stewards have signed). FROST is the open question — when adopted, transitions will carry a single threshold signature instead of an envelope.
- The exact state-encoding format. The reference implementation uses protobuf (deterministic marshalling) for compatibility with ConnectRPC. Other encodings are possible but must preserve canonical-bytes determinism.
- The exact transition-canonicalization rules
- The exact reputation-aggregation protocols

These are open because they need to be implemented, tested, and broken before they can be specified. The protocol can ship with placeholders and a roadmap for filling them in.

#### Cross-references: items tracked elsewhere

The five items above are open protocol questions. The following items appear in §5.4.6 and §5.4.7 as "not yet implemented" defenses; they are not duplicated in §11 because their resolution is tracked in `06-OPEN-QUESTIONS.md`:

- **Steward set growth via REMOVE_STEWARD bypass** (§5.4.6) — tracked in `06-OPEN-QUESTIONS.md` §7.
- **Merkle state root collision** (§5.4.6) — tracked in `06-OPEN-QUESTIONS.md` §8 (quantum-computing forward-compat via SHA-3-256 swap-in).
- **Fork/migrate races** (§5.4.6) — split-brain via two concurrent MIGRATE transitions. Tracked in `06-OPEN-QUESTIONS.md` §9.
- **Compromised wg host** (§5.4.6) — passive observation defense is via §5.1 key separation; active defense (slash-via-attest). Tracked in `06-OPEN-QUESTIONS.md` §10.
- **Gossip-level equivocation pipeline** (§5.4.6) — end-to-end "gossip evidence + slash via REMOVE_STEWARD" not yet wired. Tracked in `06-OPEN-QUESTIONS.md` §11.
- **Branch pruning policy** (§5.4.7) — production hosts SHOULD prune branches with no recent activity. Tracked in `06-OPEN-QUESTIONS.md` §12.

Readers looking for the full status of any of these should consult the cross-referenced sections of `06-OPEN-QUESTIONS.md`, not this section.

## 12. What the protocol is not

- Not a platform. No host, no user, no event is privileged by the protocol.
- Not a product. The protocol does not have a UX, a brand, a logo, or a tariff.
- Not a company. The protocol is a public good, published openly, implementable by anyone.
- Not a moderator. The protocol does not decide what groups, hosts, or users are acceptable. That is a host, directory, or community policy decision.
- Not finished. The protocol will evolve. Changes require consensus among the implementers, and the protocol is designed to be forkable if consensus fails.
