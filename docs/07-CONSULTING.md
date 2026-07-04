# 07 — Consulting Offering

**Scope:** This document defines the consulting revenue stream — what we sell, to whom, for how much, and how it makes the bootstrap math close.

**Out of scope:** The protocol (see `02-PROTOCOL.md`), the host product (see `03-PRODUCT.md`), the tariff (see `04-TARIFF.md`), the launch plan (see `05-LAUNCH.md`). This document sits alongside the launch plan as a parallel revenue stream.

---

## 1. The model in one paragraph

Engineering organizations pay $20–50k for 2-week consulting engagements to learn patterns we discovered building a federated, AI-native event platform. The patterns are the product; the federated-meetup codebase is the proof. Consulting makes the bootstrap math close: hosting margin + consulting + brokerage replaces headcount. We do not need VC funding to reach profitability if consulting covers the gap between launch and hosting-margin sustainability.

---

## 2. What we sell

### 2.1 Federation patterns

Organizations building federated systems (ActivityPub, AT Protocol, custom) pay to learn from our protocol design:
- Sovereign group identity (threshold signatures, Ed25519 multisig)
- State machine with Merkle KV + canonical bytes
- Server-to-server sync (bootstrap via GetLog, live via Subscribe)
- Fork/migrate primitives for group sovereignty
- Equivocation detection + gossip pipeline
- WireGuard mesh transport

**Target:** Engineering orgs building federated social, messaging, or collaboration platforms. Companies that would otherwise spend 6 months discovering these patterns.

### 2.2 AI-for-event-management patterns

Organizations running events (conferences, meetups, workshops) pay to learn how we use AI:
- MCP server for AI agent discovery (AIEO — AI Engine Optimization)
- LLM-readable feeds (llms.txt, structured JSON-LD)
- AI-assisted event descriptions, scheduling, translation
- Fraud detection via anomaly scoring
- Recurring-event intelligence (pattern detection, attendance prediction)

**Target:** Conference organizers, community managers, event-tech companies.

### 2.3 Self-host-at-scale patterns

Organizations running their own infrastructure pay to learn from our deployment:
- Single-binary Go server (no CGO, no Node.js, no runtime dependencies)
- SQLite WAL mode for single-host concurrency
- Caddy reverse proxy with automatic HTTPS
- Docker Compose for self-host, K3s for scale
- Opt-in persistence (in-memory for dev, SQLite for production)

**Target:** Platform engineering teams, DevOps leads, open-source communities self-hosting software.

### 2.4 Stripe Connect patterns

Organizations building marketplace or platform payments pay to learn:
- Stripe Connect Custom entity setup (EIN, KYB, bank account)
- Checkout session creation with metadata for reconciliation
- Webhook signature verification + idempotent order state machine
- Refund handling with atomic state transitions

**Target:** Fintech startups, marketplace platforms, SaaS companies adding payments.

---

## 3. Pricing

| Engagement | Duration | Price | Deliverable |
|---|---|---|---|
| Pattern review | 1 week | $20k | Code review + architecture audit of client's system against our patterns |
| Pattern implementation | 2 weeks | $35k | Working prototype implementing 1-2 patterns in client's codebase |
| Full engagement | 4 weeks | $50k | End-to-end implementation + team training + documentation |

**Terms:** 50% upfront, 50% on completion. Travel not included (remote-first).

---

## 4. The case study: Vegas Programmers fork

The Vegas Programmers succession (see `05-LAUNCH.md` §2) is the case study. When the outgoing organizer forks the group onto the federation:

1. **Before:** Group is on meetup.com. Organizer stepping down. Member list, event history, and group identity are platform-owned. No succession path that preserves sovereignty.

2. **The fork:** The organizer signs a `FORK` transition. The group's state (members, events, identity) is now a sovereign group controlled by stewards' keypairs. The incoming organizer is added as a steward via `ADD_STEWARD`.

3. **After:** The group is on the federation. The new organizer controls it. The group can migrate to any host at any time. No platform can hold it hostage.

**The consulting pitch:** "We did this for a real community with real users. Here's how. Here's what we learned. Here's what we'd do differently. $35k, 2 weeks, and your team can do it too."

---

## 5. Go-to-market

### 5.1 Channels

- **Technical blog posts** — Write deep dives on each pattern (federation, AIEO, self-host, payments). Each post is a lead generator.
- **Conference talks** — Submit to Go-focused conferences (GopherCon, dotGo) and distributed-systems conferences (QCon, Strange Loop). Each talk is a lead generator.
- **Open source credibility** — The federated-meetup repo (AGPL-3.0) is the proof. The protocol spec (02-PROTOCOL.md) is the differentiator. Engineers read the spec, see the quality, reach out.
- **Direct outreach** — Identify companies building federated systems (Mastodon, Bluesky, Threads, custom). Cold email with the case study.

### 5.2 Sales process

1. Initial call (30 min) — understand the client's system, identify which patterns apply
2. Scoping call (1 hour) — review client's architecture, propose engagement scope
3. Proposal (1 week) — written proposal with timeline, deliverables, price
4. Contract + upfront payment
5. Engagement (1-4 weeks)
6. Final report + team training session

### 5.3 Capacity

- Scott + AI agent = 2-week engagements, 1 at a time
- Target: 1 engagement per quarter in Year 1 ($80-200k/yr)
- This covers the gap between launch and hosting-margin sustainability
- Year 2: 2 engagements per quarter ($160-400k/yr)

---

## 6. Why this works

The bootstrap math:
- Hosting margin: $2-5/group/month × 1000 groups = $2-5k/month ($24-60k/yr)
- Consulting: 4 engagements × $35k avg = $140k/yr
- Brokerage: $10-20k/yr (ticketing, sponsorship, insurance referrals)
- **Total Year 1 revenue: $174-220k**
- Operating costs: $12-24k/yr (hosting + tools + legal)
- **Operating margin: $150-196k/yr**

This is a bootstrap business. No VC needed. Consulting is the bridge.

---

## 7. Risk: consulting as distraction

**Risk:** Consulting engagements consume Scott's time, slowing product development.

**Mitigation:** The AI agent does 80% of the work — code review, documentation, prototype implementation. Scott does the relationship management and the strategic decisions. A 2-week engagement uses ~20 hours of Scott's time (calls, reviews, decisions) and ~60 hours of AI agent time (code, docs, tests).

**Risk:** Consulting clients expect custom features in the product.

**Mitigation:** Engagements are explicitly about patterns, not custom features. The deliverable is knowledge transfer, not code in our repo. Client-specific code stays in the client's repo.

---

## 8. Next steps

1. Write 3 technical blog posts (one per pattern: federation, AIEO, self-host)
2. Submit 2 conference talk proposals (GopherCon, QCon)
3. Execute the Vegas Programmers fork (the case study)
4. Write the case study blog post
5. Begin direct outreach to 10 companies building federated systems
6. First engagement target: Q4 2026 or Q1 2027