# 06 — Open Questions

**Scope:** This document consolidates every open question that is not yet specified, decided, or wired end-to-end. Each item lists what is open, why it matters, and what blocks resolution. Items are grouped by category: Protocol, Discovery, and Build/Infrastructure.

**Sources:** `02-PROTOCOL.md` §11, `08-DISCOVERY.md` §8, and the v0.6 roadmap.

---

## Protocol

### 1. Threshold signature scheme: FROST vs Ed25519 multisig envelope

- **What's open:** Whether to adopt FROST (or another threshold signature scheme) as the canonical transition-signing method, or keep the current multisig envelope.
- **Why it matters:** The v1 implementation uses a multisig envelope — each steward signs with their own Ed25519 key, and the host verifies that at least `threshold` distinct stewards have signed. FROST would produce a single threshold signature per transition, making transitions more compact and faster to verify. The trade-off: FROST requires an interactive signing protocol (DKG + signing rounds), while the multisig envelope is simpler but O(n × threshold) per verification. The H-4 optimization helps but does not eliminate the scaling issue.
- **What blocks resolution:** Implementation and benchmarking of FROST against the multisig envelope at realistic steward-set sizes (3, 5, 7, 9). The DKG and signing-round coordination layer needs to be built and tested before the trade-off can be evaluated with real data.

### 2. Public directory sync protocol

- **What's open:** The exact format of the directory-to-directory sync protocol. The protocol defines name resolution (`ResolveName` RPC) but does not specify how directories replicate their name registries with each other.
- **Why it matters:** Directories are the federation's name-discovery layer. Without a sync protocol, directories operate as islands — each must be independently populated. A sync protocol would let directories mirror each other's name mappings, improving resilience and coverage.
- **What blocks resolution:** A reference directory implementation running in production with at least two instances. The sync format can be designed once we know what directory operators actually need (full replication vs. partial, push vs. pull, real-time vs. batch).

### 3. State-encoding format

- **What's open:** Whether protobuf remains the canonical state-encoding format, or whether alternative encodings are supported or preferred.
- **Why it matters:** The reference implementation uses protobuf (deterministic marshalling) for compatibility with ConnectRPC. Other encodings (CBOR, SCALE, custom binary) are possible but must preserve canonical-bytes determinism — the property that the same logical state always serializes to the same byte sequence. Without this, Merkle proofs and transition signatures break.
- **What blocks resolution:** A second implementation using a different encoding that successfully interoperates with the protobuf reference implementation. This would prove the canonical-bytes abstraction is encoding-agnostic.

### 4. Transition-canonicalization rules

- **What's open:** The exact rules for canonical bytes — which fields are included in the canonical form, what ordering is enforced, what encoding is used.
- **Why it matters:** Transitions are signed and content-addressed by their canonical bytes. If two implementations produce different canonical bytes for the same logical transition, signatures will not verify and Merkle roots will diverge. The reference implementation has these rules embedded in code, but they are not formally specified as a standalone document.
- **What blocks resolution:** Extracting the canonicalization logic from the reference implementation into a standalone specification (field list, ordering, encoding rules) and validating it against a second implementation.

### 5. Reputation-aggregation protocols

- **What's open:** Whether any reputation aggregation should be protocol-level, or whether aggregation is always a product-layer competitive service.
- **Why it matters:** The protocol supports `ATTEST` transitions — signed messages from one identity to another with a schema and payload. But the protocol does not define aggregation, scoring, or reputation computation. Aggregation is currently a product-layer competitive service ("Angie's List for events"). If aggregation stays product-level, reputations are portable (attestations travel with the identity) but scores are not standardized. If some aggregation moves to the protocol level, it risks ossifying a single reputation model.
- **What blocks resolution:** At least one product-layer aggregator running in production, plus a second aggregator with a different model. If both succeed and interoperate at the attestation level, the answer is "stay product-level." If interoperability fails because of missing protocol primitives, the protocol needs to define them.

### 6. Fork-cost tuning

- **What's open:** The actual values for fork cost parameters — fork threshold, advance-notice requirement, rationale-length minimum, and cool-down period.
- **Why it matters:** Fork cost is the mechanism that prevents trivial fork spam while keeping fork accessible as the group-sovereignty escape hatch. Too cheap and groups are unstable; too expensive and the sovereignty guarantee is hollow. The current design ships a single tunable function (the lever) with placeholder values.
- **What blocks resolution:** Real usage data. The plan is: ship the lever, instrument it, run tabletop exercises with realistic group dynamics, and tune from observed behavior. Do not reach for a fixed number until we have data from live groups.

### 7. Steward set growth via REMOVE_STEWARD bypass

- **What's open:** A steward could rotate their keys (drop and re-add with a new identity) to grow the effective steward set size (N) without going through the normal steward-addition path. This bypass is not yet modeled.
- **Why it matters:** If steward-set growth is unchecked, a malicious coordinator could inflate N to make reaching threshold harder, or to dilute other stewards' influence. The protocol may need rate limits or constraints on consecutive REMOVE_STEWARD + ADD_STEWARD cycles by the same actor.
- **What blocks resolution:** A formal model of steward-set mutation sequences, identifying which sequences produce unbounded N growth, and a rate-limit or invariant that prevents them.

### 8. Merkle state root collision resistance

- **What's open:** Whether to spec the SHA-3-256 swap-in for post-quantum forward compatibility now, or leave it as a documented but unspecified future migration.
- **Why it matters:** SHA-256 is currently secure against collision attacks. The protocol documents a migration path to SHA-3-256 for quantum-computing forward compatibility, but the actual transition mechanism (how hosts agree on the hash function, how the state root is re-computed, how proofs are re-issued) is not specified.
- **What blocks resolution:** A concrete migration proposal: a `HASH_FUNCTION` transition type or a protocol-version bump that re-roots the Merkle tree under SHA-3-256 and reissues all outstanding proofs. This is low-priority until quantum computing poses a practical threat, but the design should be ready before it is needed.

### 9. Fork/migrate races

- **What's open:** Split-brain via two concurrent `MIGRATE` transitions from different hosts. If two hosts both claim to be the new canonical host for a group, clients and mirrors need a deterministic resolution rule.
- **Why it matters:** Migration is the key anti-lock-in mechanism. If migration can produce split-brain, groups can be accidentally partitioned. The protocol currently treats this as an edge case but does not define a resolution rule (e.g., "earliest signed MIGRATE wins" or "steward threshold must explicitly revoke the other").
- **What blocks resolution:** A formal analysis of concurrent MIGRATE scenarios, a resolution rule that stewards and mirrors can verify deterministically, and a test that exercises the race condition in a multi-host integration test.

### 10. Compromised working-group host

- **What's open:** Active defense against a compromised host. Passive observation defense is handled via key separation (the host does not hold the group's private key), but active defense — where a compromised host serves forged state or censors transitions — is not fully wired.
- **Why it matters:** A compromised host can serve stale or forged state to clients who trust it as the canonical host. The protocol has the primitives for defense (equivocation log, REMOVE_STEWARD), but the "slash-via-attest" mechanism (stewards attest that the host is serving bad state, triggering a forced migration) is not specified.
- **What blocks resolution:** A specification for the slash-via-attest flow: how stewards detect a compromised host, what evidence they submit, how the evidence is verified by other hosts, and how the group is forcibly migrated away from the compromised host.

### 11. Gossip-level equivocation pipeline

- **What's open:** The equivocation log detects the data structure (two conflicting transitions with the same parent), but the end-to-end pipeline — gossip evidence propagation + automatic slash via REMOVE_STEWARD — is not wired end-to-end.
- **Why it matters:** Equivocation detection is only useful if the evidence reaches the parties who can act on it (other stewards, other hosts, directories). Without the gossip pipeline, equivocation is detected locally but not propagated, so a steward who double-signs on two hosts may not be caught.
- **What blocks resolution:** A gossip protocol for equivocation evidence (format, propagation rules, peer discovery), integration with the REMOVE_STEWARD transition, and an end-to-end test that catches and slashes a double-signing steward across two hosts.

### 12. Branch pruning

- **What's open:** Branch pruning for production hosts. Hosts serving many branches pay storage cost per branch (each branch has its own Merkle state, transition log, and equivocation log).
- **Why it matters:** Without pruning, a host that serves a large group with many branches will accumulate unbounded storage. Production hosts should prune branches with no recent activity (configurable threshold: e.g., no transitions in 90 days).
- **What blocks resolution:** A pruning policy (what "inactive" means, whether pruned branches can be resurrected, how pruning is communicated to clients), and implementation in the host's storage layer. Not in scope for v1.

---

## Discovery

### 13. Federation-wide search protocol

- **What's open:** Whether the protocol should define a cross-host search RPC, or whether search is strictly a directory-level concern.
- **Why it matters:** The current design says search is directory-level — directories aggregate host metadata and provide cross-host search. But a protocol-level search RPC would make the federation more competitive with centralized platforms, where search is built-in. The trade-off: protocol-level search adds complexity to the protocol and constrains host implementations; directory-level search keeps the protocol thin but depends on directories being well-run.
- **What blocks resolution:** A working directory implementation with cross-host search. If directory-level search proves sufficient for AI agents and human users, the protocol stays thin. If directories are unreliable or fragmented, the protocol may need to define a search surface.

### 14. MCP authentication for write operations

- **What's open:** Whether the MCP server should support write tools (RSVP, submit transition) in addition to read tools, or whether writes should always go through the ConnectRPC API directly.
- **Why it matters:** The MCP server currently exposes read tools without authentication, matching the protocol's unauthenticated read RPCs. Write operations require signed envelopes (the user signs with their private key). Supporting writes via MCP would let AI assistants handle RSVPs and transitions end-to-end, but it introduces authentication complexity into the MCP surface.
- **What blocks resolution:** A design for how signed envelopes flow through MCP (does the AI agent hold the user's key? does the MCP server relay a pre-signed envelope?), and a security analysis of key exposure in the MCP path.

### 15. Semantic search via embeddings

- **What's open:** Whether hosts should expose a semantic search endpoint using vector embeddings.
- **Why it matters:** Keyword matching is sufficient for explicit queries ("coding meetup Vegas") but not for relevance-based discovery ("find events similar to this one" or "what's happening near me this weekend that I'd like"). Semantic search would let AI assistants do relevance-based discovery, not just keyword matching. The trade-off: embedding infrastructure (model hosting, vector store, indexing pipeline) is non-trivial for self-hosters.
- **What blocks resolution:** A reference embedding implementation on one host, a decision on whether the endpoint is protocol-level or host-level, and a fallback for hosts that cannot run embedding models (e.g., a directory-level semantic search service).

### 16. Rate limiting for AI agents

- **What's open:** Whether read endpoints need per-IP or per-agent rate limiting, and whether this should be protocol-specified or host-policy.
- **Why it matters:** The protocol's rate limiter targets steward transitions (to prevent transition spam). AI agent read queries are not rate-limited at the protocol level. A burst of AI agent queries (e.g., multiple ChatGPT sessions querying the same host simultaneously) could degrade host performance. Hosts may want per-IP or per-agent rate limiting on read endpoints.
- **What blocks resolution:** Production traffic data from a live host. Once we see real AI agent query patterns, we can determine whether per-IP rate limiting is sufficient or whether a per-agent identity (e.g., an agent registry) is needed. This is likely a host-policy decision, not a protocol decision, but it should be documented in the host operator guide.

### 17. Event deduplication across hosts

- **What's open:** Documentation and tooling for event deduplication when multiple hosts serve the same group and expose feeds.
- **Why it matters:** If two hosts both serve the same group and both expose RSS/Atom/JSON feeds, an AI agent crawling the federation will see the same event twice. Without deduplication, the agent may present duplicate results to the user or double-count RSVPs. The deduplication key is `group_key + event_id`, which is canonical and stable across hosts.
- **What blocks resolution:** This is a client-side concern, not a protocol concern. What is needed is documentation in the agent integration guide (a section in `08-DISCOVERY.md` or a standalone agent guide) specifying the deduplication key and providing reference code. Low effort, but not yet written.

---

## Build / Infrastructure

### 18. Speckl wire-format reconciliation

- **What's open:** The handwritten proto file (531 lines) must be deleted and replaced by Speckl-generated output.
- **Why it matters:** Maintaining a handwritten proto alongside a Speckl schema is a source of drift and bugs. The generated output should be the single source of truth for the wire format. Until this is done, any change to the schema requires manual synchronization between two representations.
- **What blocks resolution:** Three Speckl compiler features that are not yet implemented: `field_num` annotation (to pin protobuf field numbers), `oneof` syntax (for the Transition envelope), and the Transition envelope itself (the top-level message that wraps all transition types). Budget: 4 weeks per issue, 6 weeks total Speckl budget. If the compiler features land, the handwritten proto can be replaced in a single migration.

### 19. Stripe Connect Custom entity

- **What's open:** Setting up Greybeard Holdings LLC as a Stripe Connect Custom entity.
- **Why it matters:** The tariff model (see `04-TARIFF.md`) requires payment processing. Stripe Connect Custom gives the platform control over the onboarding flow and compliance, but it requires the platform entity (Greybeard Holdings LLC) to have an EIN, a business bank account, and to pass Stripe's KYB process. Without this, hosts cannot collect payments.
- **What blocks resolution:** Legal and administrative work: obtain EIN, open business bank account, complete Stripe KYB. Estimated 4–6 weeks. This is bootstrapped in parallel with the v0 build — it does not block code, but it blocks going live with paid hosts.

### 20. Self-host channel

- **What's open:** Where self-hosters discover the project, deploy it, and get support.
- **Why it matters:** The federation's value grows with the number of hosts. Self-hosters are the long tail of hosts — small communities, local user groups, hobbyists. Without a clear discovery, deployment, and support channel, the self-host path is invisible and adoption stalls.
- **What blocks resolution:** A decision on the primary self-host channel: GitHub Discussions (low friction, ties to the repo), a Matrix room (real-time, community-driven), a Discourse forum (structured, searchable), or some combination. Must be decided before the self-host release. The deployment story (one-click deploy, Docker Compose, Helm chart) also needs to be in place.