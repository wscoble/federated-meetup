# 05 — The Launch

**Scope:** This document defines the launch plan — the seed-organizer conversations that become the first groups on the federation, the five-city rollout sequence, the unit-economics test that determines whether this is a business, the investor pitch, and the timeline with specific dates.

**Out of scope:** The protocol (see `02-PROTOCOL.md`), the host product (see `03-PRODUCT.md`), the tariff (see `04-TARIFF.md`), the discovery layer (see `08-DISCOVERY.md`). This document is the go-to-market plan that sits on top of all four.

---

## 1. The model in one paragraph

The federation launches through two seed organizers in Las Vegas, expands across five cities in the Southwest, and proves the business model with 1,000 paying groups. The protocol is the substrate; the product is the experience; the tariff is the business model; the discovery layer is the moat. This document is the sequence in which those pieces hit the market — who talks to whom, which city goes first, and when each milestone lands.

---

## 2. Seed organizer #1: Vegas Programmers (launch wedge — Group A)

Vegas Programmers is the launch wedge. It is the first group that forks onto the federation, and the succession event is the moment the federation goes from software to a live system with real users.

### 2.1 The situation

The current organizer wants to step down at the end of 2026. The group is at risk — without a succession plan, the group disperses or gets absorbed into meetup.com's churn cycle. The organizer does not want to hand the group over on meetup.com's platform, because the platform is the wrong place to do a handover: the platform owns the member list, the event history, and the group identity, and the outgoing organizer cannot guarantee that the successor will actually control the group.

### 2.2 The relationship

Scott has a direct relationship with this organizer. The handover is not a cold sales pitch — it is a conversation between two people who know each other, about a group that both care about. The organizer's problem (succession) and the federation's solution (sovereign groups with fork/migrate primitives) are the same problem seen from two sides.

### 2.3 The fork

The handover is the moment to fork onto the federation. The group's state — its member list, its event history, its identity — is the asset. The organizer may not pay anything (Group A is the free tier; see `04-TARIFF.md` §8), but the group itself is the first real data on the federation. The fork uses the protocol's `FORK` transition (see `02-PROTOCOL.md` §4) to create a new sovereign group whose initial state is a snapshot of the parent group. The outgoing organizer signs the fork as a steward; the incoming organizer is added as a steward via `ADD_STEWARD`. The threshold is set. The group is now on the federation, controlled by its stewards, portable across hosts.

### 2.4 The deadline

This is a real, time-bound event with a real budget attached. The organizer wants to step down by December 2026. That gives six months from now (July 2026) to:

1. Complete the protocol implementation (done — see `02-PROTOCOL.md`)
2. Complete the product backend (done — see `03-PRODUCT.md`)
3. Complete the MCP server (in progress — see `08-DISCOVERY.md`)
4. Build the minimum web UI needed for the succession (the event page + RSVP flow)
5. Execute the fork
6. Onboard the incoming organizer

The deadline is not aspirational. If the organizer steps down before the federation is ready, the group is lost — either to meetup.com's churn or to dissolution. The six-month window is the forcing function.

---

## 3. Seed organizer #2: LV WordPress

LV WordPress is the second seed group. It is a monthly meetup for WordPress developers and users in Las Vegas.

### 3.1 The pattern

Lower urgency than Vegas Programmers — the organizer is not stepping down — but the same pattern: the organizer wants sovereignty over the group. The member list, the event history, and the group identity belong to the organizer and the community, not to the platform. The federation offers what meetup.com cannot: the group's state is controlled by the stewards' keypair, the group can migrate to any host at any time, and the protocol guarantees that no host can hold the group hostage.

### 3.2 The role in the launch sequence

LV WordPress migrates after Vegas Programmers. The sequence matters: Vegas Programmers proves the fork (succession use case); LV WordPress proves the migration (sovereignty use case). The two seed groups cover the two highest-value protocol primitives — `FORK` and `MIGRATE` — with real users and real data, before the federation opens to groups that neither Scott nor the organizer has a prior relationship with.

---

## 4. The five-city launch target

### 4.1 The cities

| Order | City | Region | Launch quarter |
|---|---|---|---|
| 1 | Las Vegas, NV | Home market | Q3 2026 (seed groups) |
| 2 | Los Angeles, CA | Southern California | Q1 2027 |
| 3 | Phoenix, AZ | Arizona | Q1 2027 |
| 4 | Flagstaff, AZ | Northern Arizona | Q2 2027 |
| 5 | Scottsdale, AZ | Phoenix metro | Q2 2027 |

### 4.2 The geography

These are launch markets for software, not relocation targets. Scott is in an MDiv program in Las Vegas; Las Vegas is the home base. The other four cities are within driving distance (Los Angeles: 4 hours; Phoenix: 5 hours; Flagstaff: 4.5 hours; Scottsdale: 5 hours). The five-city cluster is a regional rollout that can be served by a single host operator without travel infrastructure, while still spanning two states and multiple metro areas.

### 4.3 The 100-group target

The long-term target is 100 groups across the five cities. This is not a Year 1 target — it is the steady-state goal that defines a credible regional host. The 100 groups break down roughly as:

- Las Vegas: 20-30 groups (home market, longest runway)
- Los Angeles: 30-40 groups (largest metro, highest TAM)
- Phoenix: 15-25 groups (growing tech scene)
- Flagstaff: 5-10 groups (smaller market, university presence)
- Scottsdale: 10-15 groups (Phoenix metro spillover)

Most of these groups start on the free tier. The tariff's DEFCON pattern (see `04-TARIFF.md` §2.2) means the free groups are pipeline assets — the 1% that grow into paid workshops, ticketed events, and conferences are what make the host a business.

---

## 5. The 1,000 true fans test

### 5.1 The threshold

The federation needs 1,000 groups paying for event operations to be a real business. This is the "1,000 true fans" test — not 1,000 groups total (that is the free-tier pipeline), but 1,000 groups on the Community, Pro, or Studio tier (see `04-TARIFF.md` §3). At an average of $99/mo (weighted toward Pro), 1,000 paying groups is $1.2M ARR from hosting alone, before brokerage (R2) and consulting (R3).

### 5.2 The market

There are roughly 100,000 active meetup.com groups in the United States. These are groups that have held at least one event in the past year. The total addressable market is larger (Meetup claims 300+ million members globally), but the serviceable market is the 100,000 groups that are actively organizing events.

### 5.3 The conversion math

The conversion rate from "hates meetup.com" to "switches to the federation" is the key variable. Not every group that dislikes meetup.com will switch — switching has friction (data migration, member communication, learning a new tool). The estimates:

| Scenario | Conversion rate | Timeline | Result |
|---|---|---|---|
| Optimistic | 1-2% | 3 years | 1,000-2,000 paying groups |
| Pessimistic | 0.5% | 3 years | 500 paying groups |

### 5.4 Why both scenarios work

The federation is viable in both cases because the host's marginal cost per small group is near zero (see `04-TARIFF.md` §2.1). The infrastructure is shared — canister cycles, shared database, shared mesh — and a free-tier group costs effectively nothing to serve. The paid groups cover the infrastructure and the operator; the free groups are the pipeline that feeds the paid groups.

In the pessimistic case (500 paying groups at $99/mo average = $594k ARR), the host covers infrastructure ($20-40k), founder salary ($120-150k), and operational costs ($30-50k), with modest margin. In the optimistic case (1,000-2,000 paying groups = $1.2-2.4M ARR), there is room for hires and expansion to new regions.

The 1,000 true fans test is not "can we get 1,000 groups?" It is "can we get 1,000 groups to pay, and does the cost structure survive if we only get 500?" The answer to the second question is yes, because the marginal cost of a free group is zero and the marginal cost of a paid group is dominated by Stripe Connect fees and support, both of which scale with revenue, not with headcount.

---

## 6. The investor pitch

> We are the email-of-meetups. The protocol is the substrate. Multiple hosts will run on it. We are the first host, with the first 100 groups, in 5 cities. We have a growth-stage tariff that lets small groups grow into medium groups into institutional customers. The reputation layer is the moat.

### 6.1 What each sentence means

- **"We are the email-of-meetups."** Email is an open protocol (SMTP) that anyone can implement. Multiple providers (Gmail, Fastmail, ProtonMail, self-hosted) run on it. The user's identity (their email address) is portable across providers. The federated-meetup protocol is the same model for meetup groups: an open protocol, multiple hosts, portable group identity.

- **"The protocol is the substrate."** The protocol (`02-PROTOCOL.md`) defines group keypairs, steward signatures, state replication, the WireGuard mesh, forks, migration, and mirrors. It does not define the product, the tariff, or the UX. Anyone can implement a host against the protocol. Greybeard Holdings LLC operates the first host.

- **"Multiple hosts will run on it."** The self-host parity policy (`04-TARIFF.md` §4) guarantees that the open-source product is fully featured. A second host can launch on the same protocol, serve the same groups, and compete on operations. The protocol's `MIGRATE` transition means groups can move between hosts. This is the structural property that prevents platform-level rent extraction.

- **"We are the first host, with the first 100 groups, in 5 cities."** The five-city launch (§4) is the proof that the protocol works at regional scale. 100 groups across Las Vegas, Los Angeles, Phoenix, Flagstaff, and Scottsdale is the initial footprint. The first host has a first-mover advantage in those markets — local SEO authority, local organizer relationships, local brand recognition.

- **"We have a growth-stage tariff that lets small groups grow into medium groups into institutional customers."** The tariff (`04-TARIFF.md`) is the DEFCON model: free groups are the pipeline, paid groups are the engine, institutional groups are the moat. The pricing scales with the group's success — a free weekly meetup pays $0, a ticketed workshop pays $99/mo + 2% of ticket revenue, a multi-track conference pays $299/mo + 1%. The group never hits a paywall; it hits a natural upgrade point when its own growth demands more features.

- **"The reputation layer is the moat."** The protocol's `ATTEST` transition (`02-PROTOCOL.md` §8) makes attestations portable across hosts. A user who has been attested by 50 organizers on Host A is verifiable on Host B. Reputation aggregation is a product-layer service (there can be many aggregators), but the portability of attestations is a protocol-level guarantee. The first host that builds a reputation aggregator on top of the protocol has a data network effect that compounds with every group and every attestation. A competitor host can replicate the software; it cannot replicate the reputation graph.

---

## 7. The AIEO differentiator

### 7.1 Why the discovery layer makes the pitch credible

The AIEO (AI Engine Optimization) discovery layer (`08-DISCOVERY.md`) is the differentiator that makes the investor pitch credible. The pitch says "we are the email-of-meetups" — but email did not have a moat. The moat is not the protocol (anyone can implement it) and not the product (the self-host version has full parity). The moat is the discovery layer.

### 7.2 The structural advantage

meetup.com is invisible to AI assistants because its data is behind a walled garden. When someone asks ChatGPT "find me a coding meetup in Vegas this weekend," meetup.com's data is behind an API wall that AI assistants cannot query in real-time. The federation's data is open by protocol design — any AI assistant can query any host's MCP server without authentication, resolve any group name, and return events with RSVP links.

meetup.com structurally cannot expose an MCP server without killing their walled garden. An MCP server gives AI assistants direct, real-time access to their event database — no API key, no rate limit, no platform wall. That is the opposite of their business model.

### 7.3 The timing

AI discovery is replacing search discovery. The federation is the only event platform architecture that can be queried by AI assistants without an API key. The first hosts to be discoverable by AI assistants win their local markets, because the AI assistant is the new search engine — and the federation is the only platform the AI assistant can actually search.

---

## 8. Timeline

### 8.1 Now — July 2026

- **Protocol built.** The open protocol (`02-PROTOCOL.md`) is implemented: group keypairs, steward signatures, state machine with branches, WireGuard mesh, forks, migration, mirrors, HLC ordering, equivocation detection, rate limiting, steward set bounds. All simulator tests pass.
- **Product backend built.** The host product (`03-PRODUCT.md`) is implemented: event pages, RSVP with magic-link, ticketing with Stripe Connect, organizer dashboards, order management, check-in, organizer auth with group-scoped tokens. All product tests pass.
- **MCP server built.** The discovery layer (`08-DISCOVERY.md`) is in progress: the MCP server exposes the host's read APIs as AI-assistant tools. The `/.well-known/federation` endpoint, `llms.txt`, and OpenAPI spec are being built.
- **Docs being written.** This document is part of the docs sprint that produces the full specification set (`01` through `10`).

### 8.2 Q3 2026 — Vegas Programmers succession

- **The fork onto the federation.** Vegas Programmers forks from meetup.com onto the federation using the protocol's `FORK` transition. The outgoing organizer signs the fork; the incoming organizer is added as a steward. The group's member list, event history, and identity are now sovereign — controlled by the stewards' keypair, hosted by Greybeard, portable to any host.
- **The web UI minimum.** The SvelteKit frontend ships its first slice: the public event page with magic-link RSVP. This is the minimum surface needed for the succession — attendees need to see the event page and RSVP without an account.
- **The MCP server live.** The host's MCP server is operational. AI assistants can query `vegasprogrammers.org/mcp` and discover events. The AIEO moat is real.

### 8.3 Q4 2026 — LV WordPress migration + second host launch

- **LV WordPress migration.** The second seed group migrates onto the federation using the protocol's `MIGRATE` transition. This proves the sovereignty use case (the group can leave any host at any time) and adds the second real group to the federation.
- **Second host launch.** A second host launches on the same protocol. This is the structural proof that the federation is actually federated — multiple hosts, same protocol, portable groups. The second host may be self-hosted by an organizer in another city, or operated by a partner. The self-host parity policy guarantees the second host has full feature parity.

### 8.4 Q1 2027 — Los Angeles + Phoenix expansion

- **Los Angeles launch.** The federation expands to LA. The host begins onboarding groups in the LA metro area — the largest TAM in the five-city target. LA groups start on the free tier (pipeline) and upgrade as they grow.
- **Phoenix launch.** Simultaneous with LA, the federation expands to Phoenix. The Phoenix metro has a growing tech scene and a population of meetup.com groups that are underserved by the national platform.
- **Cross-host discovery.** The federation directory (`08-DISCOVERY.md` §3.5) goes live. AI assistants can discover events across all hosts in the federation, not just a single host. The AIEO moat compounds — more hosts means more data means more AI discovery means more groups.

### 8.5 Q2 2027 — Flagstaff + Scottsdale, 100 groups target

- **Flagstaff launch.** The federation expands to Flagstaff. Smaller market than LA or Phoenix, but university presence (Northern Arizona University) and proximity to Phoenix make it a natural extension.
- **Scottsdale launch.** The federation expands to Scottsdale. Scottsdale is Phoenix metro spillover — groups that are in the Phoenix orbit but geographically and culturally distinct.
- **100 groups target.** The five-city target reaches 100 groups. This is the steady-state goal that defines a credible regional host. Most groups are on the free tier; the paying groups (Community, Pro, Studio) are the ones that have grown from free-tier pipeline into active event operations.

### 8.6 Timeline summary

| Quarter | Milestone | Key deliverable |
|---|---|---|
| Q3 2026 (Jul-Sep) | Vegas Programmers succession | First group on the federation via `FORK`; web UI minimum; MCP server live |
| Q4 2026 (Oct-Dec) | LV WordPress migration + second host | Second group via `MIGRATE`; second host operational; federation is actually federated |
| Q1 2027 (Jan-Mar) | LA + Phoenix expansion | Two new cities; federation directory live; cross-host AIEO discovery |
| Q2 2027 (Apr-Jun) | Flagstaff + Scottsdale | Five cities complete; 100 groups target |

---

## 9. What the launch plan is not

- **Not a viral growth strategy.** The launch is a regional rollout through personal relationships and local market presence. There is no viral loop, no referral program, no "invite 3 friends" mechanic. Growth is organic and relationship-driven.
- **Not a national launch.** The five-city target is a regional cluster in the Southwest. National expansion comes after the regional model is proven — not before.
- **Not a platform migration campaign.** The federation does not try to move all 100,000 meetup.com groups at once. The launch is two seed groups, then five cities, then 100 groups. The conversion math (§5) plays out over 3 years, not 3 months.
- **Not a fundraising pitch.** The investor pitch (§6) is a framing device for understanding the business model. The launch is bootstrapped — the Year 1 gap is covered by founder runway (see `04-TARIFF.md` §6). The federation does not need to raise money to launch. It needs to execute the timeline.

---

## 10. Open questions

- **Web UI scope for the succession.** What is the minimum SvelteKit surface needed for Vegas Programmers to fork and operate? The public event page + magic-link RSVP is clear. But does the succession need the organizer dashboard too, or can the incoming organizer use the RPC API directly for the first few events?
- **Second host identity.** Who operates the second host that launches in Q4 2026? Is it a self-hoster (an organizer in LA or Phoenix who wants to run their own host), or is it a Greybeard-operated host in a second region? The answer affects the federation's credibility — a second Greybeard host is less interesting than a truly independent second host.
- **LA market entry strategy.** LA is the largest metro in the five-city target. How does the federation enter LA — through a specific organizer relationship (like Vegas Programmers), through cold outreach to meetup.com organizers, or through the AIEO discovery layer (LA groups discover the federation because AI assistants surface it)?
- **Flagstaff viability.** Flagstaff is the smallest market in the five-city target. Is 5-10 groups realistic for a city of ~80,000 people? The university presence helps, but the TAM may be smaller than estimated. Should Flagstaff be replaced with a larger city (e.g., Tucson, Albuquerque)?
- **Reputation layer timeline.** The investor pitch names the reputation layer as the moat. When does the first reputation aggregator ship? The protocol's `ATTEST` transition exists, but no aggregator is built yet. The reputation layer is the long-term moat; the AIEO discovery layer is the short-term moat. The launch plan needs a milestone for when the reputation layer moves from protocol primitive to product feature.