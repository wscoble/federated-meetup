# Federated Meetup

A federation of sovereign meetup groups, with a host product on top.

Meetup.com is broken in four ways: it treats organizers as tenants instead of stewards, holds group data hostage, charges rent that small groups cannot afford, and confines discovery to a single platform. This project replaces it with a system where the *group* is a sovereign object that can be hosted anywhere, mirrored for performance, and forked when stewards disagree — and where a commercial host product (this one) sits on top of the open protocol and earns revenue from event operations, not from renting group identity.

## The three layers

This project is intentionally split into three scopes that must not blur. If they blur, the federation breaks. If they stay separate, the protocol survives.

```
┌─────────────────────────────────────────────────────────────┐
│  COMMERCIAL LAYER  —  The Host (this product)               │
│  Tariff, event operations, ticketing, reputation product,  │
│  UX, customer support, growth-stage subsidy.                │
│  Closed-source.  The thing we sell.                         │
│  See: docs/03-PRODUCT.md, docs/04-TARIFF.md                 │
└─────────────────────────────────────────────────────────────┘
                         │   runs on
                         ▼
┌─────────────────────────────────────────────────────────────┐
│  PROTOCOL LAYER  —  The Federation                          │
│  Sovereign group identity, forkable history, portable       │
│  member list, signed attestations, host-to-host interop.    │
│  Open specification.  Public good.                          │
│  See: docs/02-PROTOCOL.md                                   │
└─────────────────────────────────────────────────────────────┘
                         │   speaks to
                         ▼
┌─────────────────────────────────────────────────────────────┐
│  EVENT OPERATIONS LAYER  —  Venues, payments, travel        │
│  Ticketing, paid events, hotel blocks, venue discovery.     │
│  Provided by the host, by partners, or by the user.         │
│  See: docs/03-PRODUCT.md (operations section)               │
└─────────────────────────────────────────────────────────────┘
```

### Why three layers, not one

If commercial policy (tariff) lives in the protocol, the protocol becomes a thing a group can fork to escape the policy. The moment that happens, the federation has not federated — it has split. Email has survived 50 years because SMTP is policy-free. The product is the policy. The protocol is the mail truck.

If event operations live in the protocol, the protocol becomes opinionated about what an event is. A small-group meetup and a 30,000-person conference have different operational needs. The protocol's job is to make group state portable. What the group does with the state is the product's job.

## The launch

Vegas programmers is handing off stewardship at the end of 2026. The Las Vegas WordPress meetup is in scope next. Five-city launch: Las Vegas, Los Angeles, Phoenix, Flagstaff, Scottsdale. 100 groups target. See `docs/05-LAUNCH.md`.

## What is in this repo

- `docs/01-PROBLEM.md` — what is broken about meetup.com, in priority order
- `docs/02-PROTOCOL.md` — the open protocol spec (sovereign group, forkable history, signed attestations)
- `docs/03-PRODUCT.md` — the host product (UX, event operations, reputation layer)
- `docs/04-TARIFF.md` — growth-stage tariff: large groups subsidize small groups
- `docs/05-LAUNCH.md` — Vegas programmers succession, LV WordPress, five-city plan
- `docs/06-OPEN-QUESTIONS.md` — the things still undecided

Work is tracked in Forgejo. See the issues for this repo.

## Status

Strategy phase. Spec docs are being written. No code yet.

## Companion repos (for context, not code)

- `sscoble/scobleclaw` — the agent harness this was designed inside
- `sscoble/posts` — a related decentralized messaging project (parallel inspiration for the federation model)
- `marcus/scobleclaw` — cross-cutting architecture decisions
