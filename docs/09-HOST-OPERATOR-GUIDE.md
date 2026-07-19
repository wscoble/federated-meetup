# 09 — Host Operator Guide

**Scope:** This document covers host-side policy decisions that the protocol intentionally does not specify. The protocol (`02-PROTOCOL.md`) defines the wire format and the cryptographic invariants; it deliberately leaves operational tuning to the host operator because the right values depend on load, hardware, threat model, and jurisdiction. Each section below is a stub: what the policy is for, what the trade-offs are, and a placeholder for the actual policy. Scott (or the operator of a given host) fills in the values; this guide does not pick them.

**Status:** Stubs only. No policies are prescribed here. Each section ends with a `TODO: pick policy` line. This file exists so that the gaps noted in `06-OPEN-QUESTIONS.md` (§16, §12, §19) and `02-PROTOCOL.md` (§5.4.2) have a single home for the operator-side answer.

**Relationship to the protocol.** Nothing in this document is normative for interop. Two hosts with wildly different policies in every section below are still fully interoperable — the protocol does not care. These knobs affect a host's cost, performance, and abuse-resistance profile, not its ability to federate.

---

## 1. Read-endpoint rate limiting

**What this policy is for.** The protocol's rate limiter (see `02-PROTOCOL.md` §5.4 and `internal/ratelimit/`) targets *steward transitions* — it exists to prevent transition spam against the state machine. It does not touch read endpoints. A burst of read traffic against the public ConnectRPC surface (e.g. multiple AI agent sessions querying the same host simultaneously, a crawler walking every group's feed, or a naive client polling in a tight loop) can degrade host performance even though no transition is involved. This policy decides how a host throttles reads.

This gap is noted in `06-OPEN-QUESTIONS.md` §16 (Rate limiting for AI agents). The protocol explicitly declines to specify read-side limits because the right shape depends entirely on the host's hardware, its client mix, and its tolerance for abuse vs. its tolerance for false-positive throttling of legitimate bulk consumers.

**Trade-offs.** Per-IP limiting is the cheapest to implement and the easiest to bypass (an attacker rotates IPs). Per-agent-identity limiting (e.g. an agent registry keyed on a signed agent identity) is more precise but adds an auth surface to reads, which fights the "anyone can read" property of the public directory. Per-token limiting (issue API tokens to known agents) sits in between. Too aggressive and you throttle legitimate AI assistants that drive discovery; too lax and a single misbehaving crawler starves the host. The right answer almost certainly depends on observed production traffic, which we do not yet have.

**Policy.**
- TODO: pick policy. Candidate axes: per-IP, per-agent-identity, per-token, per-endpoint (read-group vs. read-event vs. feed-crawl). Candidate limits: tokens-per-second, burst size, daily cap. Reference: instrument `internal/ratelimit/` against read paths, observe real AI-agent query patterns for 30 days, then pick.

---

## 2. Branch pruning policy

**What this policy is for.** A group is a forest of branches (see `02-PROTOCOL.md` §3.0). Each branch carries its own Merkle KV state, transition log, equivocation log, steward history, and threshold history. Branches are never deleted by the protocol — branch IDs are never reused within a group's lifetime, and the protocol does not define a "delete branch" transition. A host that serves many branches pays storage and indexing cost per branch, forever. This policy decides when a host stops serving a branch (or stops storing its full state) for branches with no recent activity.

This gap is noted in `06-OPEN-QUESTIONS.md` §12 (Branch pruning). The protocol's stance is that pruning is a host concern, not a protocol concern: a host may prune, may keep everything, or may tier (hot branches fully indexed, cold branches archived to slower storage). The protocol only requires that a host that *claims* to serve a branch serves it correctly; it does not require a host to serve every branch.

**Trade-offs.** Prune aggressively and you break clients that follow quiet branches — a user who comes back to a branch after six months of inactivity finds it gone from your host (they can migrate to a host that still carries it, or you can re-hydrate from a peer, but both are friction). Prune never and your storage grows unboundedly with the number of branches the group has ever created, which for a long-lived active group is a real cost. The middle ground — tiered storage, where cold branches move to slower/cheaper media but are still fetchable — is more work to implement but preserves the "any branch is readable" property. The right policy depends on how cheap your cold storage is and how much you care about long-tail reads.

**Policy.**
- TODO: pick policy. Candidate axes: inactivity threshold for pruning (days/weeks/months), prune-vs-tier decision, re-hydration source (peer host vs. archive vs. reject read), and whether pruning is per-branch or per-group. Reference: measure branch activity distribution on a live host, then pick.

---

## 3. HLC drift tuning

**What this policy is for.** The protocol uses Hybrid Logical Clocks (HLCs) to order transitions across hosts without trusting wall clocks (see `02-PROTOCOL.md` §5.4.2 and the HLC discussion in the threat model). An HLC combines a physical-time component with a logical counter; the physical component is bounded by the host's wall clock but the logical component advances monotonically even when the wall clock is wrong. The tuning question is: how much drift between a host's wall clock and real time do you tolerate before you flag, reject, or alarm?

This is an operator knob because the protocol defines the HLC *algorithm* but not the *operational envelope*. A host with a clock that drifts by seconds per day will still produce correct HLCs (the logical counter catches up), but its physical-time component will diverge from peers, which makes human-readable timestamps misleading and, in extreme cases, can push the HLC's physical bound outside the window where peers can usefully compare order.

**Trade-offs.** Tight drift bounds (e.g. "alarm if wall clock is >50ms off NTP") catch problems early but require reliable NTP and will fire false alarms on networks with intermittent NTP connectivity (which is exactly the DDIL environment the simulator models — see `sim/ddil.go`). Loose bounds (e.g. "alarm only if drift exceeds 5 minutes") are robust to NTP flakiness but let misleading timestamps accumulate, and in the worst case a badly-drifted host can produce HLCs that peers treat as "in the future," which forces the peers to advance their own logical counters to catch up — a minor but real cost. The right bound depends on whether your host runs somewhere with reliable time (a datacenter with NTP) or somewhere without (a residential connection, an intermittent uplink).

**Policy.**
- TODO: pick policy. Candidate axes: drift alarm threshold (ms), drift reject threshold (the point at which the host refuses to sign transitions until clock is corrected), NTP source(s), and whether to use a hardware RTC + GPS as a backup time source. Reference: observe drift on the deployment target for 30 days, then pick.

---

## 4. Stripe KYB runbook

**What this policy is for.** The tariff model (see `04-TARIFF.md`) requires payment processing. The chosen rail is Stripe Connect Custom, which gives the platform control over the onboarding flow and compliance — but it requires the platform entity (Greybeard Holdings LLC) to be set up as a Stripe Connect Custom account, which means the entity must have an EIN, a business bank account, and must pass Stripe's Know-Your-Business (KYB) process. This runbook covers the operational steps to get and keep the platform entity in good standing with Stripe.

This gap is noted in `06-OPEN-QUESTIONS.md` §19 (Stripe Connect Custom entity). It is not a protocol question — the protocol does not know about money — but it blocks going live with paid hosts, so it lives in the operator guide.

**Trade-offs.** Stripe Connect Custom gives maximum control (custom onboarding flow, custom payout timing, custom compliance UI) at the cost of maximum operational burden: the platform is responsible for KYC on every connected host, for handling Stripe webhook delivery, for reconciling payouts, and for staying in good standing with Stripe's own KYB (which can re-trigger on changes to the entity, its directors, or its bank account). The alternative rails (Stripe Connect Express, or a non-Stripe processor) shift more of the compliance burden to Stripe or to the connected host but reduce the platform's control over the onboarding UX. The choice of rail is itself a policy decision; this runbook assumes Connect Custom because that is what the tariff doc specifies, but the operator should re-confirm before committing.

**Policy.**
- TODO: pick policy / write runbook. Candidate sections: (1) obtaining the EIN, (2) opening the business bank account, (3) completing Stripe KYB (document checklist, expected turnaround, escalation path), (4) ongoing compliance (webhook handling, KYC on connected hosts, re-KYB triggers), (5) failure modes (what to do if Stripe pauses the account). Reference: `04-TARIFF.md` for the tariff model, Stripe Connect Custom docs for the API surface, and the existing partial setup in `internal/billing/` (if any) for the implementation state.

---

## Cross-references

- `02-PROTOCOL.md` §5.4 — rate limiting (transition-side), HLCs, threat model.
- `02-PROTOCOL.md` §3.0, §3.0.1 — branches, `BRANCH_CREATE` vs `FORK`.
- `06-OPEN-QUESTIONS.md` §12 — branch pruning (protocol gap).
- `06-OPEN-QUESTIONS.md` §16 — read-endpoint rate limiting (protocol gap).
- `06-OPEN-QUESTIONS.md` §19 — Stripe Connect Custom entity (infrastructure gap).
- `04-TARIFF.md` — tariff model, payment rails.
- `08-DISCOVERY.md` §3 — read surface that this guide's §1 throttles.