# 01 — The Problem

meetup.com is not a tool for communities. It is a landlord for communities.

You build the group. You recruit the members. You run the events for a decade. And at every step, meetup.com reserves the right to change the rules, inject ads, raise the price, and — if you stop paying — delete the entire history. The "organizer" is a tenant. meetup.com is the landlord. The community is the property.

This document is the case for the prosecution. Four structural failures, one new failure that meetup.com cannot fix, and the architectural answer.

---

## 1. The group-ownership lie

You build a community of 5,000 people and meetup owns the channel. They can change the rules, inject ads, kill the group, do whatever they want. The "organizer" is a tenant, not a steward. meetup.com's fundamental lie is that it sells group-management tools to "organizers" while reserving the right to treat the group, the member list, and the history as platform property.

This is the root. Everything else flows from it. If the platform owns the group, then the platform owns the data. If the platform owns the data, then the platform sets the price. If the platform sets the price, then the platform controls the ceiling on who can discover your group. The ownership lie is not a bug — it is the business model.

The protocol's answer: **the group is a sovereign cryptographic object.** The group's identity is an Ed25519 keypair held by its stewards, not by the host. The host stores the state; the stewards control it. A group can migrate to a new host by signing a `MIGRATE` transition. A group can fork by signing a `FORK` transition. The host cannot prevent either. Sovereignty is cryptographic, not platform-dependent.

## 2. The data hostage problem

Your RSVPs, your photo archive, your group's identity all live on meetup's servers. You can't export a clean list, can't migrate, can't take it home. When a group can't pay, the history dies.

Ten years of events. Five thousand members. Every photo, every RSVP, every comment — gone, because the organizer's credit card lapsed. The data is not yours. It never was. You were renting space in someone else's database, and the lease is revocable.

The protocol's answer: **the group's state is a replicated state machine, addressable by the group's public key.** Any number of hosts can store a copy. Mirrors serve the same state from different locations. If the primary host disappears, the mirrors still serve the group's history. The state is content-addressed (transitions are identified by their hash), so mirrors can verify and deduplicate. The data belongs to the group. The host is a convenience, not a dependency.

## 3. The cost trap

Organizers pay $200+/yr, attendees get peppered with upsells, and the moment a group can't pay, the history dies. The cost is not just financial — it's the structural dependency. The platform extracts rent from communities that would exist without it.

meetup.com does not create communities. Communities create communities. meetup.com sells them back the tools to manage themselves, at a price that includes the profit margin of a platform that adds no community value. The $200/year is a tax on volunteering. The upsells are a tax on attending. The cost trap is not "meetup.com is expensive" — it's "meetup.com makes itself structurally indispensable and then charges rent."

The protocol's answer: **the tariff is a product-layer decision, not a platform tax.** Hosts set their own pricing. Some hosts are free. Some hosts charge. Some hosts are run by volunteers. Some hosts are commercial. The protocol does not encode pricing. The group's sovereignty does not depend on any single host's pricing model. If a host raises prices, the group can migrate. The cost trap is broken by making the host substitutable.

## 4. The discovery ceiling

It's a walled garden. The only people who see your event are the people already on meetup, and the platform's algorithm decides who that is. Local communities, niche interests, the people who'd actually show up, are invisible.

meetup.com has 20 years of SEO authority and zero reach beyond its own walls. If you're not on meetup.com, you can't find meetup.com events. If you're on meetup.com, the algorithm decides what you see. The discovery ceiling is not "meetup.com has bad search" — it's "meetup.com is a closed network that controls visibility as a feature, not a bug." The algorithm is the landlord's gatekeeper.

The protocol's answer: **discovery is open by design.** Every host emits `schema.org/Event` JSON-LD, RSS/Atom/JSON feeds, sitemaps, and a public API. Every host exposes a `/.well-known/federation` endpoint for AI agents. Every host serves an MCP server that any AI assistant can query without an API key. Federation-wide discovery is peer-to-peer — hosts list their peers, agents crawl the graph. No single party controls the namespace. No algorithm decides who sees your event.

## 5. The AIEO wall — the failure meetup.com cannot fix

meetup.com is invisible to AI assistants. When someone asks ChatGPT "find me a coding meetup in Vegas this weekend," meetup.com's data is behind an API wall that AI cannot query in real-time. A federated platform with open APIs and an MCP server is AIEO-native by design — any AI assistant can query any host without an API key.

meetup.com structurally cannot do this without abandoning their walled-garden business model. An MCP server gives AI assistants direct, real-time access to their event database — no API key, no rate limit, no platform wall. That is the opposite of their business model. They'd be handing their data to AI competitors who could then recommend events without sending the user to meetup.com. meetup.com would have to kill the thing that makes them money.

The federation has no wall to dismantle. The data is open by protocol design. Every host can expose an MCP server. Every AI assistant can connect to every host. The total engineering effort to make a host AIEO-native is days, not months. When someone asks "find me a coding meetup this weekend in Vegas," the AI assistant probes the host, calls `find_events`, and returns real-time results with RSVP links. No API key. No walled garden. No "please log in to meetup.com."

AI discovery is replacing search discovery. meetup.com has 20 years of SEO authority and zero AIEO presence. The federation has zero SEO authority and native AIEO. The question is whether AI discovery replaces search discovery in 2 years or 5 — and in either case, meetup.com's walled garden is a liability, not an asset.

## 6. The structural answer

The protocol/product/tariff separation is the architectural answer to meetup.com's structural problems:

| Layer | What it is | What it solves |
|---|---|---|
| **Protocol (open)** | The cryptographic substrate. Groups are sovereign keypairs. State is a replicated state machine. Anyone can implement. | The group-ownership lie. Sovereignty is cryptographic, not platform-dependent. |
| **Product (closed)** | The host. The UX, the features, the brand. Hosts compete on quality, not on data lock-in. | The data hostage problem. A group can migrate to a different host without losing its state. |
| **Tariff (product-layer)** | Pricing. A host decision, not a platform tax. Free hosts, paid hosts, volunteer hosts — all valid. | The cost trap. The group's existence does not depend on any single host's pricing. |

And the discovery layer — open APIs, MCP servers, feeds, JSON-LD — solves the discovery ceiling and the AIEO wall simultaneously. meetup.com cannot follow without abandoning the business model that creates the problems.

---

## The five-minute version

meetup.com owns your community, holds your data hostage, charges you rent for the privilege, controls who can find you, and is invisible to the AI assistants that are replacing search. Every one of these is a structural consequence of the walled-garden business model. You cannot fix them with better features or lower prices. You can only fix them by removing the platform from the center.

The federation removes the platform from the center. The group is sovereign. The data is portable. The host is substitutable. The discovery is open. The AI assistant is a first-class client.

The community owns the community. Everything else is infrastructure. Infrastructure should compete, not extract.