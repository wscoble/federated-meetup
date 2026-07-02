# 03 — The Product

**Scope:** This document defines the host product — the closed-source software that Greybeard Holdings LLC builds on top of the open protocol defined in `02-PROTOCOL.md`. The protocol is the substrate; the product is the experience. Anyone can implement a host against the protocol. This document describes the host we actually ship.

**Out of scope:** The protocol (see `02-PROTOCOL.md`), the tariff (see `04-TARIFF.md`), the discovery layer (see `08-DISCOVERY.md`). If a question is "what does the *protocol* do?", it does not belong here.

---

## 1. The model in one paragraph

Greybeard Holdings LLC builds and operates "Federated Meetup" — a host product that sits on top of the open federated-meetup protocol. The protocol handles federation, steward signatures, state replication, and the WireGuard mesh. The product handles everything a human touches: the event page, RSVP, ticketing, payments, organizer dashboards, email reminders, iCal export, and the web UI. The product is closed-source. The protocol is open. Anyone can run a host — Greybeard's host is the reference implementation, but it is not the only host. The product's job is to be the host that organizers choose because it is the easiest, the most joyful, and the one where someone else handles the sysadmin work.

---

## 2. Brand and entity

- **Parent entity:** Greybeard Holdings LLC
- **Working name:** Federated Meetup
- **Product:** The Federated Meetup host — a managed host for meetup organizers, built on the open federated-meetup protocol

---

## 3. Dual-mode hosting

The product ships in two hosting modes. The same software runs in both modes. The difference is who operates it and what they pay for.

### 3.1 Self-host

Anyone can run a host. The open-source protocol layer ships as a Docker Compose stack that includes:
- The ConnectRPC server (client-facing API)
- The WireGuard mesh node (federation transport)
- The state machine (group replication, transitions, snapshots)
- The product daemon (ticketing, RSVP, organizer APIs)

Self-hosters get the full product — every feature, no gating. They bring their own Stripe keys if they want integrated monetization. They handle their own backups, updates, and federation peering.

### 3.2 Paid-host

Greybeard operates a managed host for organizers who don't want to sysadmin. Organizers point their custom domain at Greybeard's infrastructure, and Greybeard handles:
- Server uptime and TLS
- Federation peering and mesh health
- Stripe Connect integration (managed onboarding, webhook routing, payout reconciliation)
- Backups and disaster recovery
- Email delivery for reminders and magic-links
- The web UI (SvelteKit, mobile-first)

### 3.3 Self-host parity policy

**All features are free in self-host.** There is no feature gating. Paid-host buys:
- Convenience (no sysadmin work)
- SLA (uptime, support response time)
- Integrated monetization (Stripe Connect with managed onboarding — self-hosters can use their own Stripe keys, but Greybeard's managed flow is smoother)

No feature is withheld from self-host. If Greybeard ships it in paid-host, it ships in self-host. The protocol is free as in speech; the product is free as in beer if you operate it yourself. You pay for the operation, not the software.

---

## 4. Pricing tiers

| Tier | Price | Target |
|---|---|---|
| **Free** | $0 | Small groups, new organizers, pipeline assets |
| **Community** | $29/mo | Active local groups with regular events |
| **Pro** | $99/mo | Groups with ticketed events, multiple organizers, higher volume |
| **Studio** | $299/mo | Multi-group operators, agencies running events for several communities |

**Small groups pay $0.** They are pipeline assets — some grow into paying groups. The free tier is not a trial; it is a permanent home for small groups.

**Large groups pay a percentage of event revenue** (growth-stage tariff). The details are in `04-TARIFF.md`. The principle: the host captures value when the group captures value. A group running a $10,000 conference pays more than a group running a free weekly meetup.

---

## 5. Event packages

The product supports four event packages. Each package is a bundle of features scoped to an event type. A group selects a package when creating an event. The package determines what the organizer dashboard shows and what the attendee experience includes.

### Package A: Free Community

- Basic event page (title, description, time, location)
- RSVP (going / not going)
- No ticketing
- No recurrence
- Target: small local meetups, community gatherings

### Package B: Recurring Club

- Everything in Package A
- Recurring events (RRULE-based, with exception dates)
- Member management (roles: owner, organizer, moderator, member)
- iCal export (per-group and per-event)
- Target: clubs, hobby groups, recurring weekly/monthly meetups

### Package C: Ticketed Workshop

- Everything in Package B
- **Full Stripe Connect integration** (checkout sessions, payouts, refunds)
- Ticket tiers (early bird, regular, supporter, custom)
- Capacity management (per-ticket and per-event, atomic sold-count)
- Venue management
- Public event page with mobile-first RSVP
- Email reminders (pre-event, post-event)
- iCal export
- **This is what v0 ships.**

### Package D: Conference

- Everything in Package C
- Multi-track scheduling
- Sponsor management (tiers, logos, attendee access)
- Vendor marketplace (verified vendors, referral fees, ratings)
- Target: conferences, conventions, large-scale events
- **Status: future. The proto schema defines the data types (`Vendor`, `Sponsorship`), but the product code does not implement this package yet.**

### Package mapping in the code

The proto enum `Package` in `proto/federated_meetup/product/v1/state.proto` defines all four packages:

```
PACKAGE_FREE_COMMUNITY = 1     // Package A
PACKAGE_RECURRING_CLUB = 2     // Package B
PACKAGE_TICKETED_WORKSHOP = 3  // Package C (v0 ships this)
PACKAGE_CONFERENCE = 4         // Package D (future)
```

The `Event` message carries a `Package` field. The `SetGroupPackagePayload` transition type lets a group change its default package. The `Group` message carries both a `Package` and a `HostingTier`, so a group's package and its hosting tier are independent — a Free-tier group can run a Ticketed Workshop event (the tier governs the subscription; the package governs the event features).

---

## 6. Joy-first North Star

The product is designed around a single principle: **joy**. Joy is not a vibe; it is a set of instrumented metrics with targets. Every feature decision is evaluated against these metrics. If a feature makes any of these numbers worse, it does not ship.

### 6.1 Instrumented metrics

| Metric | Target | What it measures |
|---|---|---|
| Attendee time-to-RSVP | < 60 seconds, mobile | How long from "I want to go" to "I'm going" on a phone |
| Organizer time-to-event-created | < 5 minutes | How long from "I want to host something" to "event is live" |
| Organizer weekly active minutes | < 30 min/week | How much time the organizer spends in the dashboard per week |
| Support tickets per paid instance per month | < 1 | How often a paid organizer needs human help |
| NPS (organizer cohort, 90 days) | > 50 | Net Promoter Score at 90-day mark |

These are not aspirations. They are product constraints. The time-to-RSVP target drives the no-account-required, magic-link RSVP flow. The time-to-event-created target drives the organizer onboarding flow. The weekly-active-minutes target drives the "AI does the busywork" operating model (see §8).

### 6.2 Joy, defined negatively

Joy is easier to define by what we don't do. The product does NOT:

- **No algorithm.** There is no recommendation engine, no feed ranking, no "you might like this group." Events are listed chronologically. Groups are listed alphabetically. Discovery is by search, by feed, by AI assistant — not by algorithm.
- **No required account for attendees.** An attendee RSVPs with their email and a magic-link. No password, no profile, no "create an account." The magic-link token is the session.
- **No app to install.** The attendee experience is a web page. Mobile-first, responsive, no download required.
- **No promoted events.** No organizer can pay to have their event appear above another. No sponsored placements. No boosted events.
- **No email marketing by default.** The product sends transactional email (RSVP confirmation, reminder, magic-link). It does not send marketing email. It does not send "events you might like." It does not send "we miss you." Organizers cannot buy email blasts.
- **No "engagement" notifications.** No push notifications. No "X people RSVP'd since you did." No "this event is trending." The product does not manufacture urgency.
- **No dark patterns.** No "only 3 spots left!" countdown timers. No "14 people are looking at this event right now." No pre-checked boxes. No confirmshaming ("No, I don't want to attend this great event").

These are not preferences. They are product constraints. Violating any of them is a bug.

---

## 7. The attendee experience

The attendee experience is the joy surface. It is where the time-to-RSVP metric lives.

### 7.1 The RSVP flow (what exists in the code)

The `SubmitRsvp` RPC in `internal/product/service.go` implements the core flow:

1. Attendee provides `event_id` and `email` (and optionally `name`).
2. The service creates an RSVP with status `GOING` and a timestamp.
3. The service generates a 32-char hex magic-link token and stores the token→email mapping.
4. The response returns the RSVP and a magic-link URL (`https://app.federatedmeetup.com/rsvp?token=<token>`).
5. If the attendee already has an RSVP, the service returns the existing RSVP with a fresh magic-link token (no duplicate RSVPs).

The `CancelRsvp` RPC verifies the magic-link token against the email, then sets the RSVP status to `NOT_GOING`.

The `ListMyRsvps` RPC verifies the magic-link token and returns all RSVPs for that email — so an attendee can see their upcoming events without an account.

**No account. No password. No profile. Email + magic-link = session.**

### 7.2 The ticket purchase flow (what exists in the code)

The `PurchaseTicket` RPC implements the paid-attendee flow:

1. Attendee provides `ticket_id`, `attendee_email`, and `quantity` (defaults to 1).
2. The service reads the ticket to compute the amount (price × quantity) and currency.
3. The service calls the payment provider's `CreateCheckoutSession` — if `STRIPE_SECRET_KEY` is set, this creates a real Stripe Checkout Session; otherwise a mock provider returns a fake URL.
4. The service calls `AtomicPurchaseTicket` on the store, which atomically checks capacity, increments the ticket's sold count, and creates a `PENDING` order. If the ticket is sold out, it returns `FAILED_PRECONDITION`.
5. The response returns the `order_id` and the Stripe Checkout URL. The attendee is redirected to Stripe to complete payment.

### 7.3 The public event page (what exists in the code)

The `GetPublicEvent` RPC returns an event (by slug or event_id), its tickets, the RSVP count, and the capacity. No auth required. This is the data behind the public event page — the web UI that renders this data does not exist yet (see §10).

### 7.4 What the attendee experience does NOT do yet

- **Web UI** — the SvelteKit frontend is not started. The RPCs exist; the rendering does not.
- **Public event page with magic-link RSVP** — the RPCs exist (`GetPublicEvent`, `SubmitRsvp`), but the HTML page that an attendee visits in their browser does not.
- **iCal export** — the proto defines `RecurrenceRule` with RRULE and timezone, but no iCal feed endpoint exists.
- **Email reminders** — no email sending infrastructure is wired. The magic-link is generated and returned in the API response, but no email is sent.

---

## 8. AI as operating principle

AI is not a feature in the product. It is how the product runs. The host is designed to operate with minimal human staffing because AI absorbs the spiky, repetitive, low-judgment work.

### 8.1 What AI does

AI replaces or augments these functions in the host:

| Function | What AI does |
|---|---|
| Tier-1 support | Answers organizer and attendee questions via the MCP server / chat surface. Escalates to humans only when it cannot resolve. |
| Moderation | Detects spam, abuse, and policy violations in event descriptions and group metadata. Flags for human review when uncertain. |
| Organizer onboarding | Walks a new organizer through event creation. Suggests title, description, time, capacity based on the organizer's stated intent. |
| Description writing help | Drafts event descriptions from a few bullet points. Organizer edits and approves. |
| Scheduling suggestions | Proposes optimal times based on historical attendance patterns and group timezone. |
| Email drafting | Composes transactional emails (reminders, confirmations) in the group's voice. |
| Fraud detection | Flags suspicious purchase patterns, chargeback risk, refund abuse. |
| Translation | Translates event pages for attendees in different locales. |
| Recurring-event intelligence | Detects attendance trends across recurring events, suggests schedule adjustments. |

### 8.2 Why AI is an operating principle, not a feature

AI reduces staffing costs. A host that needs a 24/7 support team cannot offer a $29/mo tier. AI replaces the support team for tier-1 issues, the moderation team for routine content review, and the onboarding team for new-organizer hand-holding. This is what makes the pricing sustainable.

AI also absorbs spiky work. Event creation is bursty (organizers create events in clusters). Support is bursty (an event cancellation triggers a wave of refund requests). AI handles the spikes without staffing up, and escalates to humans only when judgment is needed.

AI is not a marketing claim. The product does not say "AI-powered." AI is how the host runs lean. The attendee never sees an AI. The organizer might interact with AI-assisted drafting and scheduling, but the AI is a tool, not a personality.

---

## 9. The organizer experience

### 9.1 Organizer auth (what exists in the code)

Organizer authentication uses **organizer tokens with group scoping**. The store maintains a map of `organizer token → group_id` (in `internal/product/store.go`). Every organizer-scoped RPC validates the token against the group that owns the target resource:

- `validateOrganizerTokenForGroup` — checks the token is valid for the given group_id (used by `GetOrganizerDashboard`)
- `validateOrganizerTokenForEvent` — resolves event → group, then checks the token (used by `ListAttendees`, `CreateTicket`, `ListOrders`, `CheckInAttendee`)
- `validateOrganizerTokenForOrder` — resolves order → ticket → event → group, then checks the token (used by `RefundOrder`)

A token valid for group A cannot access resources in group B. Cross-group access returns `CodePermissionDenied`. Missing token returns `CodeUnauthenticated`. This is enforced at the service layer and covered by 18 boundary tests in `internal/product/auth_test.go`:

- **6 tests: no token → `CodeUnauthenticated`** (one per organizer RPC: `GetOrganizerDashboard`, `ListAttendees`, `CreateTicket`, `RefundOrder`, `ListOrders`, `CheckInAttendee`)
- **6 tests: cross-group token → `CodePermissionDenied`** (same six RPCs, valid token for group A used against group B's resources)
- **6 tests: valid token for correct group succeeds** (same six RPCs, valid token for the correct group)

### 9.2 The organizer dashboard (what exists in the code)

The `GetOrganizerDashboard` RPC aggregates:
- Upcoming (non-cancelled) events for the group
- Total RSVPs across all upcoming events
- Total revenue (sum of `COMPLETED` orders across all upcoming events)
- Pending actions (list of orders with `PENDING` status)

### 9.3 Ticket management (what exists in the code)

The `CreateTicket` RPC lets an organizer create a ticket for an event with:
- Name, tier type (early bird, regular, supporter, custom)
- Price (amount in cents + currency)
- Capacity (0 = unlimited)
- Sale window (starts_at, ends_at)

The store tracks `sold` count per ticket and enforces capacity atomically.

### 9.4 Order management (what exists in the code)

The `ListOrders` RPC paginates orders for an event. The `RefundOrder` RPC supports:
- **Full refund** (amount = 0): order status → `REFUNDED`, ticket sold count decremented
- **Partial refund** (amount > 0): order status → `PARTIALLY_REFUNDED`, refunded_amount accumulated. If cumulative refunds reach the full amount, transitions to `REFUNDED` and decrements sold count.
- **Idempotency**: refunding an already-fully-refunded order returns `CodeFailedPrecondition` without double-decrementing sold count.

### 9.5 Check-in (what exists in the code)

The `CheckInAttendee` RPC marks a RSVP's `attended` field to `true`. Organizer-token scoped to the event's group.

### 9.6 What the organizer experience does NOT do yet

- **Web UI** — the organizer dashboard RPC exists, but the SvelteKit dashboard UI does not.
- **AI-assisted onboarding** — no AI integration is wired into the event creation flow yet.
- **iCal export** — no endpoint generates iCal feeds.
- **Email reminders** — no email sending is wired.

---

## 10. What exists vs. what's planned

### 10.1 What the product code already does (as of this writing)

| Capability | Where it lives | Status |
|---|---|---|
| Stripe Connect integration | `internal/payment/stripe.go` | ✅ Working — creates real Stripe Checkout Sessions when `STRIPE_SECRET_KEY` is set |
| Webhook signature verification | `internal/payment/webhook.go` | ✅ Working — verifies Stripe-Signature, handles `checkout.session.completed`, `checkout.session.expired`, `charge.failed`, `charge.dispute.created`, `charge.refunded` |
| Mock payment provider | `internal/payment/` (mock) | ✅ Working — fallback for local dev when Stripe key is not set |
| Create checkout session | `internal/product/service.go::PurchaseTicket` | ✅ Working |
| Create order (pending) | `internal/product/store.go::AtomicPurchaseTicket` | ✅ Working — atomic capacity check + sold increment + order creation |
| Complete order | `internal/product/store.go::AtomicCompleteOrder` | ✅ Working — idempotent transition on Stripe webhook |
| Mark order failed | `internal/product/store.go::AtomicMarkOrderFailed` | ✅ Working — idempotent, decrements sold count |
| Full refund | `internal/product/store.go::AtomicRefundOrder` | ✅ Working — idempotent, decrements sold count |
| Partial refund | `internal/product/store.go::AtomicRefundOrder` | ✅ Working — accumulates refunded_amount, transitions to REFUNDED at full amount |
| Mark order disputed | `internal/product/store.go::AtomicMarkOrderDisputed` | ✅ Working — called by webhook on `charge.dispute.created` |
| Ticket creation | `internal/product/service.go::CreateTicket` | ✅ Working |
| Ticket listing | `internal/product/service.go::ListTickets` | ✅ Working |
| RSVP submission (magic-link) | `internal/product/service.go::SubmitRsvp` | ✅ Working |
| RSVP cancellation | `internal/product/service.go::CancelRsvp` | ✅ Working |
| RSVP listing (by email) | `internal/product/service.go::ListMyRsvps` | ✅ Working |
| Public event read | `internal/product/service.go::GetPublicEvent` | ✅ Working |
| Upcoming events listing | `internal/product/service.go::ListUpcomingEvents` | ✅ Working — paginated, filters cancelled |
| Organizer dashboard | `internal/product/service.go::GetOrganizerDashboard` | ✅ Working |
| Attendee listing | `internal/product/service.go::ListAttendees` | ✅ Working |
| Order listing | `internal/product/service.go::ListOrders` | ✅ Working — paginated |
| Attendee check-in | `internal/product/service.go::CheckInAttendee` | ✅ Working |
| Organizer auth tokens (group-scoped) | `internal/product/service.go::validateOrganizerToken*` | ✅ Working |
| Auth boundary tests (18 tests) | `internal/product/auth_test.go` | ✅ Working — 6 unauthenticated, 6 cross-group denied, 6 valid |
| Payment edge tests | `internal/product/payment_edge_test.go` | ✅ Working |
| Concurrency tests | `internal/product/concurrent_test.go` | ✅ Working |
| Integration tests | `internal/product/integration_test.go` | ✅ Working |
| Service tests | `internal/product/service_test.go` | ✅ Working |
| Product daemon | `cmd/fedmeetup-product/main.go` | ✅ Working — standalone HTTP server, mounts ProductService + Stripe webhook handler |
| Product proto (RPC surface) | `proto/federated_meetup/product/v1/rpc.proto` | ✅ Defined — 12 RPCs: 3 public reads, 3 attendee writes, 1 attendee purchase, 5 organizer |
| Product proto (state schema) | `proto/federated_meetup/product/v1/state.proto` | ✅ Defined — Group, Event, Ticket, Order, RSVP, GroupMember, RecurrenceRule, Vendor, Sponsorship, plus 11 product transition types |
| Webhook adapter | `internal/product/webhook_adapter.go` | ✅ Working — bridges Store's atomic methods to webhook interface |

### 10.2 What the product does NOT do yet

| Capability | Status | Notes |
|---|---|---|
| Web UI (SvelteKit, mobile-first) | ❌ Not started | The RPC surface is complete; the rendering layer is the next major build |
| Public event page with magic-link RSVP | ❌ Not started | The RPCs exist (`GetPublicEvent`, `SubmitRsvp`); the HTML page does not |
| iCal export | ❌ Not started | Proto defines `RecurrenceRule` with RRULE + timezone; no iCal feed endpoint exists |
| Email reminders | ❌ Not started | No email sending infrastructure wired; magic-link is returned in API response but not emailed |
| Self-host Docker Compose (full federation stack) | ❌ Not started | The product daemon runs standalone; the full federation stack (WireGuard mesh, state replication, directory) is not packaged as Docker Compose yet |
| MCP server | 🔧 Being built now | See `docs/08-DISCOVERY.md` — the MCP server exposes host read APIs as AI-assistant tools |
| AI-assisted organizer onboarding | ❌ Not started | AI operating principle is the design; AI integrations are not yet wired |
| AI tier-1 support | ❌ Not started | Same |
| AI moderation | ❌ Not started | Same |
| Package D (Conference) | ❌ Future | Proto schema defines `Vendor` and `Sponsorship` types; product code does not implement multi-track, sponsor management, or vendor marketplace |

---

## 11. The product proto surface

The product exposes its RPCs via ConnectRPC over HTTP/2. The proto files are in `proto/federated_meetup/product/v1/`. These are **not** part of the open protocol standard — they are the closed-source product's API surface.

### 11.1 RPCs (from `rpc.proto`)

```
service ProductService {
  // Public reads (no auth)
  rpc GetPublicEvent(GetPublicEventRequest) returns (GetPublicEventResponse);
  rpc ListUpcomingEvents(ListUpcomingEventsRequest) returns (ListUpcomingEventsResponse);
  rpc ListTickets(ListTicketsRequest) returns (ListTicketsResponse);

  // Attendee writes (magic-link auth)
  rpc SubmitRsvp(SubmitRsvpRequest) returns (SubmitRsvpResponse);
  rpc CancelRsvp(CancelRsvpRequest) returns (CancelRsvpResponse);
  rpc ListMyRsvps(ListMyRsvpsRequest) returns (ListMyRsvpsResponse);

  // Attendee purchases
  rpc PurchaseTicket(PurchaseTicketRequest) returns (PurchaseTicketResponse);

  // Organizer (organizer-token auth)
  rpc GetOrganizerDashboard(GetOrganizerDashboardRequest) returns (GetOrganizerDashboardResponse);
  rpc ListAttendees(ListAttendeesRequest) returns (ListAttendeesResponse);
  rpc CreateTicket(CreateTicketRequest) returns (CreateTicketResponse);
  rpc RefundOrder(RefundOrderRequest) returns (RefundOrderResponse);
  rpc ListOrders(ListOrdersRequest) returns (ListOrdersResponse);
  rpc CheckInAttendee(CheckInAttendeeRequest) returns (CheckInAttendeeResponse);
}
```

### 11.2 Auth model

| Surface | Auth method | How it works |
|---|---|---|
| Public reads | None | `GetPublicEvent`, `ListUpcomingEvents`, `ListTickets` require no auth |
| Attendee writes | Magic-link token | `SubmitRsvp` generates a token; `CancelRsvp` and `ListMyRsvps` verify the token against the email |
| Attendee purchases | None (payment = auth) | `PurchaseTicket` requires no token — the Stripe Checkout URL is the auth surface; the order is created as PENDING and completed via webhook |
| Organizer RPCs | Organizer token (group-scoped) | Token is validated against the group that owns the target resource. Cross-group access is denied. |

### 11.3 State schema (from `state.proto`)

The product state schema defines the domain objects that the product manages on top of the protocol's group state:

- **Group** — `group_id`, `canonical_name`, `display_name`, `hosting_mode`, `hosting_tier`, `package`, `custom_domain`, `member_count`
- **Event** — `event_id`, `group_id`, `title`, `description`, `starts_at`, `ends_at`, `location`, `venue_id`, `visibility`, `capacity`, `package`, `paid`, `slug`, `cancelled`, `recurrence`
- **Ticket** — `ticket_id`, `name`, `tier_type`, `price` (Money), `capacity`, `sold`, `sale_starts_at`, `sale_ends_at`
- **Order** — `order_id`, `ticket_id`, `attendee_email`, `status`, `stripe_session_id`, `amount_paid` (Money), `refunded_amount` (Money), `created_at`, `refunded_at`
- **RSVP** — `event_id`, `user_email`, `status`, `created_at`, `attended`
- **GroupMember** — `group_id`, `user_email`, `role`, `joined_at`
- **RecurrenceRule** — `rrule`, `exceptions`, `timezone`
- **Vendor** — `vendor_id`, `name`, `category`, `website`, `rating`, `verified`, `referral_fee_percent` (Package D)
- **Sponsorship** — `sponsor_id`, `name`, `tier`, `logo_url`, `website`, `attendee_access` (Package D)

The schema also defines 11 product transition types (`ProductTransitionType`) for an append-only product transition log: `CREATE_TICKET`, `UPDATE_TICKET`, `PURCHASE_TICKET`, `REFUND_ORDER`, `CREATE_RECURRENCE`, `ADD_EXCEPTION`, `SET_GROUP_PACKAGE`, `SET_HOSTING_TIER`, `UPDATE_VISIBILITY`, `SET_MEMBER_ROLE`, `MARK_ATTENDED`. These are distinct from the protocol's transition types — they govern product-layer state, not federation state.

### 11.4 Order state machine

```
                    ┌─────────────┐
                    │   PENDING   │
                    └──────┬──────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
     checkout.session   charge.failed   │
       .completed       checkout.session │
              │          .expired        │
              ▼            │            │
        ┌──────────┐      ▼            │
        │COMPLETED │  ┌───────┐        │
        └──────────┘  │FAILED │        │
                      └───────┘        │
                                       │
              charge.dispute.created   │
              ──────────────────────────►
                             ┌──────────┐
                             │ DISPUTED │
                             └──────────┘

     From COMPLETED or PENDING:
        RefundOrder (amount=0)  → REFUNDED
        RefundOrder (amount>0)  → PARTIALLY_REFUNDED
                                   (→ REFUNDED when cumulative = full)
```

All state transitions are atomic and idempotent. The store methods (`AtomicCompleteOrder`, `AtomicMarkOrderFailed`, `AtomicRefundOrder`, `AtomicMarkOrderDisputed`) hold a write lock for the entire check-and-mutate sequence, eliminating TOCTOU races. Calling a transition on an already-terminal order is a no-op (returns `alreadyTerminal=true`).

---

## 12. Relationship to the protocol

The product is a layer on top of the protocol, not a replacement for it.

| Layer | What it handles | Open/closed |
|---|---|---|
| **Protocol** (`02-PROTOCOL.md`) | Group keypairs, steward signatures, state machine, forks, branches, WireGuard mesh, migration, mirrors, HLC ordering, equivocation detection | Open source (AGPL-3.0) |
| **Product** (this document) | Event pages, RSVP, ticketing, payments, organizer dashboards, email, iCal, web UI, AI operations, pricing, support | Closed source (Greybeard Holdings LLC) |
| **Discovery** (`08-DISCOVERY.md`) | SEO, AIEO, MCP server, feeds, sitemaps, llms.txt, OpenAPI | Open standard (hosts MUST conform to be discoverable) |

The protocol does not know about tickets, orders, Stripe, or RSVP magic-links. The product does. The protocol handles federation; the product handles the human experience. A self-hoster gets the product code (all features, no gating) and the protocol code (federation, replication). A paid-host customer gets both, operated by Greybeard.

The product's proto (`proto/federated_meetup/product/v1/`) is separate from the protocol's proto (`proto/federated_meetup/v1/`). The product proto defines product-layer state (tickets, orders, RSVPs, packages, hosting tiers). The protocol proto defines federation-layer state (groups, stewards, transitions, branches). The two do not share messages — the product is a strict superset of functionality built on top of the protocol's infrastructure.

---

## 13. What the product is not

- **Not a platform.** The product does not own the group's member list, event history, or identity. The group's state is sovereign (controlled by steward keys, portable across hosts via `MIGRATE`). The product is a convenience layer, not a captivity layer.
- **Not a social network.** No profiles, no feeds, no follow graphs, no "activity." The product is a tool for organizing events, not a destination for browsing.
- **Not an advertising platform.** No promoted events, no sponsored groups, no ad inventory. Revenue comes from hosting fees and event-revenue percentage, not from attention selling.
- **Not a data broker.** Attendee emails are used for transactional communication (RSVP confirmation, reminders) and nothing else. No data sales, no audience building, no "analytics" products built on attendee data.
- **Not a walled garden.** The protocol guarantees that any group can migrate to any host at any time. The product's job is to be good enough that groups don't want to leave — not to make leaving impossible.