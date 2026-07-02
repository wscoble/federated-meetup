# 04 — The Tariff

**Scope:** This document defines the growth-stage tariff — how the federation makes money. It covers the pricing model, the self-host parity policy, the three revenue streams, the bootstrap math, the Stripe Connect legal structure, and the market segments that map to each revenue path.

**Out of scope:** The protocol (see `02-PROTOCOL.md`), the host product (see `03-PRODUCT.md`), the discovery layer (see `08-DISCOVERY.md`). The tariff is a product-layer decision, not a protocol-layer decision. This separation is load-bearing.

---

## 1. The core principle: the tariff is NOT in the protocol

The protocol is free as in speech. It does not encode commercial policy. It does not encode pricing, tiers, or payment rails. The protocol document (§5.3) explicitly lists "impose commercial terms" as something a host *may* do — not something the protocol *does*.

Different hosts can run different tariffs. A community-run host can charge $0 forever. A commercial host can charge subscription fees plus a percentage of ticket revenue. A nonprofit host can run on grants. The protocol does not pick. The protocol's job is to make groups portable (via `MIGRATE`) so that no host can hold a group hostage over pricing.

This separation is what prevents platform-level rent extraction. meetup.com's business model is platform capture — the platform owns the group's member list, event history, and channel, and charges rent for access. The federation's architecture makes that impossible: the group's state is sovereign (controlled by the stewards' keypair), the group can migrate to any host at any time, and the protocol is open. A host that charges too much loses its groups to cheaper hosts. The tariff is competitive, not extractive.

---

## 2. The growth-stage tariff model

### 2.1 The thesis

Small groups pay $0. They are pipeline assets, not customers. The 1% who grow will pay for all 100% of them.

Large groups pay a percentage of event revenue (1-3% of ticket revenue, capped). A group that runs a 500-person conference with $50 tickets generates $25,000 in revenue; a 2% take is $500. A group that runs a free weekly meetup generates $0 and pays $0. The model scales with the group's success, not with the group's existence.

### 2.2 The DEFCON pattern

The model is explicitly modeled on DEFCON's growth pattern. DEFCON started as a small gathering of hackers in the early 1990s, was fed by the smaller meetup ecosystem (2600 meetings, local hacker groups, HOPE), and grew into a 30,000-attendee conference. The small groups that fed DEFCON were never "customers" of DEFCON — they were the pipeline. The federation's tariff follows the same logic: the free tier is the pipeline, the paid tiers are the conference.

The federation does not need every group to pay. It needs the groups that grow into conferences, workshops, and festivals to pay enough to cover the groups that stay small. The marginal cost of hosting a small group is near zero (canister cycles, shared infra), so the free tier is not a cost center — it is a customer acquisition channel.

---

## 3. Pricing tiers

| Tier | Price | Key features |
|---|---|---|
| **Free** | $0/mo | Basic event page, RSVP, no ticketing, community support |
| **Community** | $29/mo | Recurring events, member management, iCal export |
| **Pro** | $99/mo | Ticketed events, Stripe Connect integration, email reminders, priority support |
| **Studio** | $299/mo | Multi-track events, sponsor management, vendor marketplace, dedicated support |

### 3.1 The tier logic

- **Free** is the wedge. Every group starts here. The organizer who is burned out and just wants a basic event page with RSVP gets it for nothing. No credit card, no friction, no lock-in. The group's data is sovereign — the organizer can migrate to a paid tier on this host, to a different host, or to self-host, at any time.

- **Community** is for groups that have stabilized and want recurring events and member management. $29/mo is less than meetup.com's $24.99/mo (or $29.99/mo for the "Pro" organizer tier), and the group gets portability and federation that meetup.com cannot offer.

- **Pro** is the engine. This is where ticketing and payments come in. A group that is running paid workshops or small conferences needs Stripe Connect integration, email reminders, and priority support. $99/mo is the price point where the host's margin covers the cost of serving dozens of free-tier groups.

- **Studio** is the moat. Multi-track events, sponsor management, and vendor marketplace are features that conference organizers pay thousands of dollars for today (Cvent, Whova, Bizzabo). $299/mo is a fraction of that, and the group still owns its data.

### 3.2 The revenue-share on ticket sales

In addition to the subscription, Pro and Studio tiers pay 1-3% of ticket revenue, capped at a per-event maximum. The percentage scales inversely with ticket price: a $500 conference ticket pays 1%, a $20 workshop ticket pays 3%. The cap prevents a single blockbuster event from generating disproportionate revenue (and disproportionate risk).

This is the DEFCON model in microcosm: the groups that run free events pay nothing on tickets (because there are no tickets), and the groups that run paid events pay a small percentage of the revenue they generate.

---

## 4. Self-host parity policy (binding)

### 4.1 The rule

**ALL features are free in self-host. No feature gating. No "upgrade to unlock."**

A self-hoster gets the exact same product as a paid Pro customer. The only difference is who runs the infrastructure. Studio features (multi-track, sponsor management, vendor marketplace) are available in self-host. Pro features (ticketing, Stripe Connect, email reminders) are available in self-host. There is no artificial paywall in the open-source code.

### 4.2 What paid hosting buys

Paid hosting = **convenience** (we run the server, we handle updates, we manage backups) + **SLA** (uptime guarantee, incident response) + **integrated monetization** (Stripe Connect setup, payout handling, tax compliance, dispute management).

A self-hoster who wants ticketing has to set up their own Stripe account, handle their own payouts, and deal with their own tax compliance. A Pro-tier customer gets all of that handled by the host. That is the value proposition: not locked-in features, but operational burden removed.

### 4.3 Why this policy is binding

This policy is what makes the federation credible. If the open-source code was a crippled version of the paid product, the federation would be a freemium trap — "open source" as a marketing label, not a structural commitment. Groups would see the paywall and know that "free" means "demo."

By making self-host fully featured, the federation makes the paid tiers compete on operational value, not on feature extraction. A group that is willing to run its own server pays $0. A group that wants the server run for them pays $29-299/mo. The choice is about who runs the infrastructure, not about what features are available.

This is the structural difference between the federation and meetup.com. meetup.com's product is the only way to access meetup.com's features. The federation's product is one way to access the federation's features — the other way is self-host, and it is the same product.

---

## 5. Three revenue streams

### R1: Paid hosting margin (year 1+)

The primary revenue stream. The host charges subscription fees ($29-299/mo) plus a percentage of ticket revenue (1-3%, capped). The margin comes from the difference between what customers pay and what it costs to serve them.

**Unit economics:**
- Average revenue per paying instance: $99/mo (weighted toward Pro tier)
- Marginal cost per small group: near zero (canister cycles, shared infra)
- The $0 tier is the wedge, the medium tier is the engine, the institutional tier is the moat

**Year 1:**
- 8-12 paying customers
- $60-100k ARR
- Gap covered by founder runway

**Year 2:**
- 100 paying customers
- $120k ARR
- Hosting margin covers all infrastructure costs plus founder salary

The growth curve is the DEFCON curve: the free tier feeds the paid tier. Every free group is a candidate for upgrade when it grows. The host's job is to make the upgrade path natural — when a free group runs its first paid workshop, the ticketing feature is right there, and the 2% take is a rounding error compared to the revenue the workshop generates.

### R2: External-relationship brokerage (year 2+)

The host is the trusted intermediary between groups and the event-services ecosystem. The host knows the group, the event, the venue, and the attendees. That position of trust is monetizable.

**Brokerage verticals:**
- **Insurance:** liability insurance for workshop organizers. A group running a hands-on soldering workshop needs liability coverage. The host brokers a policy, takes a commission.
- **Venue marketplace:** venue booking for event organizers. The host maintains a directory of vetted venues with availability calendars and pricing. Organizers book through the host; the host takes a booking fee.
- **AV equipment rentals:** audio/visual equipment for conferences and workshops. The host partners with local AV providers, handles the booking, takes a margin.
- **Vendor directory:** caterers, photographers, security, accessibility services. The host maintains the directory; vendors pay for placement or the host takes a per-booking fee.

**Year 2 target:** $60-120k ARR

The brokerage revenue stream is downstream of R1. You cannot broker insurance to a group that is not already on your host. The free tier feeds the paid tier; the paid tier feeds the brokerage. Each revenue stream is a stage in the group's lifecycle: free → paid → brokerage.

### R3: Consulting on patterns (year 2+)

Engineering organizations pay to learn from the federation's architecture. The federation is a novel system — sovereign groups, WireGuard mesh federation, AI-native discovery, Stripe Connect at the edge — and the patterns it develops are valuable to other engineering teams building similar systems.

**Consulting offerings:**
- Federation patterns: how to design a protocol that supports sovereignty, migration, and forks without a central authority
- AI-for-event-management patterns: how to expose an MCP server, structure data for AIEO, and make a service discoverable by AI assistants
- Self-host-at-scale patterns: how to run a hosted product where the self-host version has full feature parity and the paid version competes on operations, not features
- Stripe Connect patterns: how to implement marketplace payments at the edge with Custom accounts, KYB, and payout handling

**Pricing:** $20-50k per 2-week engagement

**Year 2 target:** $50-140k ARR from 2-4 engagements/year

This is the revenue stream that makes the bootstrap math close. Hosting margin alone (R1) covers infrastructure and a modest founder salary. Brokerage (R2) adds operational revenue. Consulting (R3) adds high-margin revenue that scales with knowledge, not with headcount. The combination of all three replaces what a seed-funded startup would spend on headcount.

---

## 6. Bootstrap math

### Year 1: survival

| Stream | Target |
|---|---|
| R1: Hosting | $60-100k ARR |
| R2: Brokerage | $0 (not yet launched) |
| R3: Consulting | $0 (not yet launched) |
| **Total** | **$60-100k ARR** |

The gap between $60-100k ARR and a sustainable runway is covered by the founder's personal runway. The Year 1 goal is not profitability — it is product-market fit with 8-12 paying customers who validate the pricing model and the self-host parity promise.

### Year 2: close

| Stream | Target |
|---|---|
| R1: Hosting | $120k ARR (100 paid instances) |
| R2: Brokerage | $60-120k ARR |
| R3: Consulting | $50-140k ARR (2-4 engagements) |
| **Total** | **$230-380k ARR** |

Bootstrap closes at: **100 paid instances + brokerage scale + first consulting engagement**. At $230k ARR, the business covers infrastructure ($20-40k), founder salary ($120-150k), and operational costs ($30-50k), with modest margin. At $380k ARR, there is room for a first hire.

The bootstrap math is intentionally conservative. The low end ($230k) assumes 100 Pro-tier customers, one brokerage vertical at modest scale, and two consulting engagements. The high end ($380k) assumes a mix of Pro and Studio customers, two brokerage verticals at scale, and four consulting engagements. Neither end assumes a viral growth event or a large enterprise contract.

---

## 7. Stripe Connect Custom under Greybeard Holdings LLC

### 7.1 The legal entity

The host product operates under **Greybeard Holdings LLC**. Stripe Connect is set up as a Custom account type, which gives the host full control over the onboarding flow, payout timing, and dispute management — at the cost of taking on KYB (Know Your Business) compliance.

**Setup requirements:**
- EIN (Employer Identification Number) for Greybeard Holdings LLC
- Business bank account
- KYB (Know Your Business) verification through Stripe
- Terms of service and privacy policy, Greybeard-scoped

**Timeline:** 4-6 weeks of legal/admin work, bootstrapped in parallel with the v0 build. The Stripe Connect setup does not block the protocol or the host product — the v0 host can launch with free-tier groups and manual ticketing before Stripe is live. But Stripe Connect must be operational before the Pro tier opens for paying customers.

### 7.2 Why Custom, not Standard or Express

- **Standard** gives Stripe control over the onboarding flow. The host's brand is diluted. Not acceptable for a product that competes on UX.
- **Express** gives Stripe even more control and shifts compliance to Stripe, but at the cost of a generic onboarding experience that does not match the host's brand. Also not acceptable.
- **Custom** gives the host full control over the onboarding flow, the dashboard experience, and the payout timing. The host owns the relationship with the organizer. The cost is KYB compliance — the host is responsible for verifying each organizer's business identity. This is the right tradeoff for a product whose value proposition includes "we handle the operational burden."

### 7.3 The organizer flow

1. Organizer creates a group on the host (free tier).
2. Organizer upgrades to Pro ($99/mo) — this is the host's subscription revenue.
3. Organizer creates a ticketed event. The host prompts the organizer to complete Stripe Connect onboarding (Custom account, KYB).
4. Organizer sells tickets. Stripe processes the payment. The host's 1-3% take is deducted from the payout. The organizer receives the remainder via Stripe payout.
5. The host handles disputes, refunds, and chargebacks on the organizer's behalf (Custom account = host is the merchant of record).

The host is the merchant of record. The organizer is the sub-merchant. The host takes the subscription fee (R1) plus the ticket-revenue percentage (R1) plus the brokerage commission on any venue/insurance/AV services booked through the host (R2).

---

## 8. Market segments and revenue mapping

The market analysis identified five segments. Each maps to a different point in the tariff:

| Segment | Profile | Role in the tariff |
|---|---|---|
| **Group A** | Burned organizer with succession problem | **Launch wedge.** These are the first free-tier users. They adopt because the federation solves their problem (sovereign group, no dead-owner lock-in, fork/branch primitives). They pay $0. They are the pipeline. Vegas programmers are the prototype. |
| **Group B** | Organizer whose meetup grew into a small business | **Revenue stream.** These are the first Pro-tier customers. They run paid workshops, need ticketing, need Stripe Connect. They pay $99/mo + 2% of ticket revenue. They are the engine. |
| **Group C** | Ideologically opposed to meetup.com | **Marketing voice, not business model.** These users adopt for philosophical reasons and self-host. They pay $0. They are the most vocal advocates and the best source of word-of-mouth. They do not directly generate revenue, but they generate trust and credibility. |
| **Group D** | Host-less communities on Discord/Facebook/Slack | **Long-tail TAM.** These communities have never used a dedicated event platform. They adopt the free tier because it is better than a Discord pinned message for organizing events. Some fraction grows into Group B over time. The rest stay free forever and cost nothing to serve. |
| **Group E** | Institutional organizers — universities, libraries, corporate ERGs | **Second revenue stream, downstream of B.** These are the Studio-tier customers. They run multi-track events, need sponsor management, need vendor marketplace. They pay $299/mo + 1% of ticket revenue. They are the moat. They adopt after the host has proven the model with Group B. |

### 8.1 The adoption sequence

1. **Group A** adopts the free tier (launch wedge).
2. **Group B** upgrades to Pro (first revenue).
3. **Group C** self-hosts and advocates (credibility and distribution).
4. **Group D** adopts the free tier (TAM expansion).
5. **Group E** adopts Studio (second revenue stream, downstream of B proving the model).

The tariff is designed so that each segment's adoption naturally feeds the next. Group A validates the product. Group B validates the pricing. Group C validates the philosophy. Group D expands the TAM. Group E validates the enterprise tier.

---

## 9. What the tariff is not

- **Not a protocol tax.** The protocol is free. No host, no group, no user pays to implement or use the protocol. The tariff is what one specific host (Greybeard Holdings LLC) charges for its hosted product. Other hosts can charge more, less, or nothing.
- **Not a data tax.** The host does not sell attendee data, organizer data, or group data. The data is sovereign — it belongs to the group. The host is a custodian, not an owner.
- **Not an ad model.** The host does not show ads. There is no "promoted events" slot. There is no sponsored placement in search or feeds. The brokerage revenue (R2) comes from transactional commissions on services booked through the host, not from advertising.
- **Not a lock-in model.** The self-host parity policy (§4) ensures that any group can leave at any time with full functionality. The protocol's `MIGRATE` transition ensures that any group can move to a different host with full state. The tariff competes on operational value, not on captivity.

---

## 10. Open questions

- **Tier boundaries.** Where exactly does Community end and Pro begin? Is email reminders a Pro feature or a Community feature? The current tier definitions are growth-stage hypotheses, not final. They will be tuned based on which features drive upgrade conversions.
- **Ticket-revenue percentage.** Is 1-3% the right range? Too low and the host leaves money on the table. Too high and organizers bypass the host's Stripe integration and handle payments externally. The cap (per-event maximum) needs to be set at a level that does not penalize blockbuster events.
- **Brokerage pricing.** What commission does the host take on insurance, venue, and AV bookings? The host is a marketplace, not a reseller — the pricing needs to be transparent to organizers and sustainable for vendors.
- **Consulting capacity.** Consulting is high-margin but not scalable beyond the founder's time. When does the host hire a second consultant? Does consulting become a separate business unit, or does it sunset as the hosting and brokerage revenue scales?
- **Enterprise pricing.** Group E (institutions) may need custom pricing — volume discounts, annual contracts, SLA guarantees, dedicated infrastructure. The Studio tier ($299/mo) is the starting point, but the enterprise sales motion may require a separate pricing sheet.