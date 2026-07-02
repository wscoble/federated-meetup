# 08 — Discovery: SEO, AIEO, and the Open Agent Surface

**Scope:** This document defines how groups and events are discovered — by humans (search engines, feeds, directories), by AI assistants (MCP servers, LLM-readable content), and by programmatic clients (OpenAPI, ConnectRPC). The discovery layer is the commercial moat: meetup.com is invisible to AI assistants because its data is behind a walled garden. The federation is open by design, and that openness is the advantage.

**Out of scope:** The protocol (see `02-PROTOCOL.md`), the host product (see `03-PRODUCT.md`), the tariff (see `04-TARIFF.md`). This document specifies the discovery surface that hosts expose and the standards they conform to.

---

## 1. The thesis in one paragraph

meetup.com has 20 years of SEO authority and zero AIEO presence. When someone asks ChatGPT "find me a coding meetup in Vegas this weekend," meetup.com's data is behind an API wall that AI assistants cannot query in real-time. The federation's data is open by protocol design — any AI assistant can query any host's public API without authentication, resolve any group name, and return events with RSVP links. The federation is the AIEO-native event platform. The first hosts to be discoverable by AI assistants win their local markets, because AI discovery is replacing search discovery, and the federation is the only architecture that can be queried without an API key.

---

## 2. SEO: distribute, don't concentrate

### 2.1 The problem

meetup.com's SEO strategy is a single domain (`meetup.com`) with thin, templated pages duplicated across 100,000 cities. Google has been systematically devaluing this pattern since the Helpful Content Update. A frontal SEO assault on "coding meetup las vegas" against meetup.com's domain authority is a multi-year losing battle.

### 2.2 The federation's structural SEO advantage

The federation distributes content across many hosts, each with its own domain, its own local content, and its own authority signal. `vegasprogrammers.org` builds local SEO authority that `meetup.com/vegas-programmers/` cannot match because:

- It is a real local site with real local content, not a template page on a national domain
- It can earn local backlinks (local news, local blogs, local universities) that feed its own domain authority
- It can produce local content (organizer profiles, event recaps, community history) that Google rewards
- It is not competing with 100,000 other cities for the same domain's crawl budget

### 2.3 What every host MUST emit

These are protocol-level requirements for hosts that want to be discoverable:

#### 2.3.1 schema.org/Event JSON-LD

Every event published by a host MUST render a `schema.org/Event` JSON-LD block on its public event page. The host already has the data in protobuf — a thin renderer emits the structured data.

Required fields:
- `@type`: "Event"
- `name`: event title
- `startDate`: ISO 8601 datetime
- `endDate`: ISO 8601 datetime (if set)
- `location`: `schema.org/Place` with `name` and `address`
- `eventStatus`: "EventScheduled" or "EventCancelled"
- `eventAttendanceMode`: "OfflineEventAttendanceMode" or "OnlineEventAttendanceMode"
- `url`: canonical event URL on this host
- `description`: event description (if provided)
- `organizer`: `schema.org/Organization` or `schema.org/Person` referencing the group
- `offers`: `schema.org/Offer` (if ticketed — price, availability, URL)

Optional fields:
- `image`: event image URL
- `maximumAttendeeCapacity`: from event capacity
- `remainingAttendeeCapacity`: computed from RSVPs

The JSON-LD block is injected into the event page's `<head>` as `<script type="application/ld+json">`. This is table stakes — without it, Google Events, Bing, and other search engines cannot index the event in their event discovery surfaces.

#### 2.3.2 RSS 2.0 + Atom 1.0 + JSON Feed

Every group MUST expose feed endpoints:

- `GET /feeds/{canonical_name}.rss` — RSS 2.0 feed of upcoming events
- `GET /feeds/{canonical_name}.atom` — Atom 1.0 feed
- `GET /feeds/{canonical_name}.json` — JSON Feed (https://jsonfeed.org/version/1.1)

Every host MUST expose aggregate feeds:
- `GET /feeds/all.rss` — RSS 2.0 feed of all upcoming events on this host
- `GET /feeds/all.atom` — Atom 1.0 feed
- `GET /feeds/all.json` — JSON Feed

Feed items include: event title, description, start/end time, location, group name, canonical event URL, RSVP count, and a `schema.org/Event` JSON-LD block embedded in the item's `<content>` (RSS) or `<content>` (Atom) for AI parsers that consume feeds.

Feeds are both human-subscribable and AI-parseable. They are the substrate that traditional aggregators (Feedly, Inoreader) and AI crawlers (GPTBot, ClaudeBot, Perplexity) consume. A host without feeds is invisible to the feed-reading ecosystem.

#### 2.3.3 sitemap.xml

Every host MUST expose a sitemap at `GET /sitemap.xml` listing all public event pages and group pages. The sitemap is updated as events are created and cancelled.

- `GET /sitemap.xml` — index of all sitemaps
- `GET /sitemap-events.xml` — all event URLs
- `GET /sitemap-groups.xml` — all group profile URLs

#### 2.3.4 robots.txt

Every host MUST expose `GET /robots.txt` that:
- Allows all major search engine crawlers
- Explicitly allows AI crawlers (GPTBot, ClaudeBot, PerplexityBot, AppleBot, Google-Extended)
- Points to the sitemap
- Points to `/.well-known/federation` (see §3.1)
- Points to `/llms.txt` (see §3.2)

```
User-agent: *
Allow: /

# AI crawlers explicitly allowed
User-agent: GPTBot
Allow: /

User-agent: ClaudeBot
Allow: /

User-agent: PerplexityBot
Allow: /

User-agent: Google-Extended
Allow: /

Sitemap: https://vegasprogrammers.org/sitemap.xml
```

---

## 3. AIEO: the open agent surface

### 3.1 `/.well-known/federation` — host discovery endpoint

Every host MUST expose a well-known endpoint that describes the host and its groups in a machine-readable format. This is the AIEO equivalent of `robots.txt` — it tells AI agents "here is what I serve, here is how to query me."

```
GET /.well-known/federation
Content-Type: application/json
```

Response:
```json
{
  "protocol": "federated-meetup/v1",
  "host": {
    "name": "Vegas Programmers Host",
    "url": "https://vegasprogrammers.org",
    "description": "Local host serving Las Vegas programming and tech groups.",
    "geographic_area": "Las Vegas, NV",
    "coordinates": { "lat": 36.17, "lng": -115.14 },
    "language": "en"
  },
  "groups_endpoint": "/api/v1/list-groups",
  "events_endpoint": "/api/v1/list-events",
  "resolve_name_endpoint": "/api/v1/resolve-name",
  "connectrpc_endpoint": "/api/v1/",
  "mcp_endpoint": "/mcp",
  "openapi_spec": "/openapi.json",
  "feeds": {
    "rss": "/feeds/all.rss",
    "atom": "/feeds/all.atom",
    "json": "/feeds/all.json"
  },
  "group_count": 12,
  "event_count_this_week": 5,
  "federation_peers": [
    "https://phoenixdevs.org",
    "https://saltlakecoder.org"
  ]
}
```

AI agents probe this endpoint to discover what a host serves. The `geographic_area` and `coordinates` fields let agents filter hosts by location. The `mcp_endpoint` and `openapi_spec` fields tell the agent how to query the host programmatically.

### 3.2 `/llms.txt` — LLM-readable host summary

Every host MUST expose an `llms.txt` file at the root. This is a growing standard (https://llmstxt.org) for providing AI-readable content. The file describes the host, its groups, and its events in a format optimized for LLM consumption — plain text, no HTML, no templating.

```
# Vegas Programmers Host

> Local host serving Las Vegas programming and tech groups.

## Groups

### vegas-programmers
- Display name: Vegas Programmers
- Description: Weekly meetup for programmers in the Las Vegas valley.
- Events: Thursdays, 7pm, Goldwell Open Air Museum
- URL: https://vegasprogrammers.org/groups/vegas-programmers

### lv-wordpress
- Display name: Las Vegas WordPress
- Description: Monthly meetup for WordPress developers and users.
- Events: First Saturday, 10am, Las Vegas Library
- URL: https://vegasprogrammers.org/groups/lv-wordpress

## API

This host speaks the federated-meetup v1 protocol.
- List groups: GET /api/v1/list-groups
- List events: GET /api/v1/list-events?group_key=<pubkey>
- Resolve name: GET /api/v1/resolve-name?canonical_name=<name>
- OpenAPI spec: GET /openapi.json
- MCP server: POST /mcp

## Feeds

- RSS: /feeds/all.rss
- Atom: /feeds/all.atom
- JSON Feed: /feeds/all.json
```

### 3.3 OpenAPI spec — the AI agent API contract

Every host MUST expose an OpenAPI 3.1 spec at `GET /openapi.json` that describes the ConnectRPC API in standard OpenAPI format. This is the "developer documentation" for AI agents — an AI assistant can read the spec and know exactly how to query the host.

The OpenAPI spec is auto-generated from the protobuf definitions (`proto/federated_meetup/v1/rpc.proto`). ConnectRPC already maps gRPC methods to HTTP paths; the OpenAPI spec formalizes this mapping for tools that don't speak ConnectRPC natively.

Key endpoints in the spec:

- `GET /api/v1/get-group` — get a group by key or name
- `GET /api/v1/get-event` — get a single event
- `GET /api/v1/list-events` — list events for a group (paginated)
- `GET /api/v1/list-groups` — list all groups on this host (paginated)
- `GET /api/v1/resolve-name` — resolve a canonical name to a group key
- `POST /api/v1/submit-transition` — submit a signed transition (steward operations)
- `POST /api/v1/submit-user-action` — submit a user-signed action (RSVP, attest)
- `GET /api/v1/get-log` — get transition log (paginated)
- `GET /api/v1/get-snapshot` — get state snapshot
- `GET /api/v1/subscribe` — server-streaming live transition events (SSE or gRPC stream)

The spec includes:
- Request/response schemas (JSON mappings of the protobuf messages)
- Authentication notes: read endpoints are unauthenticated; write endpoints require signed transitions (not bearer tokens)
- Pagination conventions (cursor + page_size)
- Error codes and their meanings

### 3.4 MCP server — the killer feature

**Model Context Protocol (MCP)** is the emerging standard for AI assistants to connect to external data sources. Claude, ChatGPT, and other AI assistants support MCP servers as first-class data providers.

Every host SHOULD expose an MCP server at `POST /mcp` (or `ws://` for the SSE transport). The MCP server exposes the host's read APIs as MCP tools that any AI assistant can call:

**Tools exposed:**

1. `list_groups` — List all groups on this host
   - Parameters: `name_contains` (optional string), `page_size` (optional int)
   - Returns: array of `{ group_key, canonical_name, display_name }`

2. `list_events` — List upcoming events for a group
   - Parameters: `group_key` (required string — hex pubkey), `page_size` (optional int)
   - Returns: array of `{ event_id, title, starts_at, location, capacity, rsvp_count, cancelled }`

3. `get_event` — Get details for a single event
   - Parameters: `group_key` (required), `event_id` (required)
   - Returns: `{ event_id, title, starts_at, ends_at, location, capacity, rsvp_count, cancelled, description }`

4. `resolve_name` — Resolve a canonical name to a group
   - Parameters: `canonical_name` (required string, e.g. "vegas-programmers")
   - Returns: `{ group_key, hosts: ["https://vegasprogrammers.org", ...] }`

5. `get_group` — Get group details
   - Parameters: `group_key` (optional) or `canonical_name` (optional)
   - Returns: `{ group_key, canonical_name, display_name, steward_count, threshold, event_count, member_count }`

6. `find_events` — Search for events by location and/or interest
   - Parameters: `location` (optional string, e.g. "Las Vegas"), `interest` (optional string, e.g. "programming"), `when` (optional string, e.g. "this weekend")
   - Returns: array of events from all groups on this host matching the criteria
   - Note: this is a host-level search, not a federation-wide search. For federation-wide search, the AI agent queries multiple hosts (discovered via `/.well-known/federation` on each host).

**Why MCP is the moat:**

meetup.com structurally cannot expose an MCP server without killing their walled garden. An MCP server gives AI assistants direct, real-time access to their event database — no API key, no rate limit, no platform wall. That is the opposite of their business model. They'd be handing their data to AI competitors who could then recommend events without sending the user to meetup.com.

The federation has no walled garden to protect. The data is open by protocol design. Every host can expose an MCP server, and every AI assistant can connect to every host. When someone asks "find me a coding meetup this weekend in Vegas," the AI assistant:

1. Probes `https://vegasprogrammers.org/.well-known/federation` — discovers the host serves Las Vegas
2. Connects to `https://vegasprogrammers.org/mcp` — MCP server
3. Calls `find_events` with `location="Las Vegas"` and `interest="programming"` and `when="this weekend"`
4. Gets back real-time event data with RSVP links
5. Returns the result to the user, with a direct link to RSVP on the host

No API key. No rate limit. No walled garden. No middleware. The AI assistant is a first-class client of the federation.

### 3.5 Federation directory — cross-host discovery

A single host only knows about its own groups. For AI assistants to find events across the entire federation, they need to discover all hosts. Two mechanisms:

#### 3.5.1 Directory hosts

A directory host is a host that aggregates group metadata from many hosts. It exposes the same `/.well-known/federation` endpoint, plus an extended API:

- `GET /api/v1/directory/hosts` — list all known hosts in the directory
- `GET /api/v1/directory/search?location=Las+Vegas&interest=programming` — search across all known hosts for groups matching criteria

The directory does not store events — it stores host URLs and group metadata (name, description, location, host URL). The AI agent then queries the individual host's MCP server for event details.

Anyone can run a directory. The protocol does not privilege any directory. A directory is just a host that has chosen to aggregate metadata from its federation peers.

#### 3.5.2 Federation peer discovery

Each host's `/.well-known/federation` endpoint includes a `federation_peers` field listing other hosts it knows about. An AI agent can crawl the federation by:

1. Start at any known host (e.g., from a directory or a hardcoded seed)
2. Read `/.well-known/federation` to get `federation_peers`
3. Repeat for each peer, building a graph of all hosts

This is the same model as Fediverse instance discovery — no central registry, just peer-to-peer knowledge.

---

## 4. The AI agent discovery flow (end to end)

Here is the complete flow when a user asks an AI assistant "find me a coding meetup in Vegas this weekend":

```
User → AI Assistant: "find me a coding meetup in Vegas this weekend"

AI Assistant:
  1. Discover hosts serving Las Vegas:
     - Check its known directory hosts (e.g., directory.fedmeetup.org)
     - Or crawl from a seed host's /.well-known/federation
     - Filter by geographic_area or coordinates matching Las Vegas
     - Result: vegasprogrammers.org serves Las Vegas

  2. Connect to the host's MCP server:
     - POST https://vegasprogrammers.org/mcp
     - Call find_events(location="Las Vegas", interest="programming", when="this weekend")
     - Result: [
         {
           event_id: "evt-2026-07-10",
           title: "Vegas Programmers Weekly",
           starts_at: "2026-07-10T19:00:00-07:00",
           location: "Goldwell Open Air Museum, Las Vegas, NV",
           capacity: 50,
           rsvp_count: 23,
           cancelled: false,
           group: "vegas-programmers",
           url: "https://vegasprogrammers.org/events/evt-2026-07-10"
         }
       ]

  3. Return to user:
     "Found Vegas Programmers Weekly this Thursday at 7pm at Goldwell Open
     Air Museum. 23 of 50 spots filled. You can RSVP at
     https://vegasprogrammers.org/events/evt-2026-07-10"

User → AI Assistant: "RSVP me to that"

AI Assistant:
  4. The user's identity is already on a host (e.g., the same host or
     another federated host). The AI assistant calls:
     POST /api/v1/submit-user-action
     with a user-signed RSVP envelope (the user's key signs the RSVP,
     the host verifies the signature against the user's public key).

  5. The RSVP is applied to the group's state machine. The user's
     public key is now in the RSVP list. The state root advances.

  6. Confirm to user:
     "You're RSVP'd. See you Thursday at 7pm."
```

No API key. No walled garden. No "please log in to meetup.com." The AI assistant is a first-class federation client.

---

## 5. Why meetup.com cannot do this

meetup.com's business model is platform capture. Their revenue comes from:
1. Organizers paying $200+/yr to host groups on meetup.com
2. Attendees being funneled through meetup.com's UI (ads, upsells, data collection)
3. The platform owning the group's member list, event history, and channel

Exposing an MCP server or an open API that AI assistants can query without authentication would:
- Let AI assistants recommend events without sending users to meetup.com → kills ad/upsell revenue
- Let organizers export their member list programmatically → kills data hostage revenue
- Let third-party clients compete with meetup.com's UI → kills platform lock-in

meetup.com would have to abandon their business model to become AIEO-native. They won't. They'll add a chatbot to their website and call it "AI-powered," but the data stays behind the wall.

The federation has no wall to dismantle. The data is open by protocol design. The MCP server is a thin wrapper over the existing ConnectRPC API. The `llms.txt` is a text file. The OpenAPI spec is auto-generated. The total engineering effort to make a host AIEO-native is days, not months.

---

## 6. Implementation priority

### Phase 1: AIEO-native host (weeks 1-2)

1. **OpenAPI spec** — auto-generate from protobuf, serve at `/openapi.json`
2. **`llms.txt`** — generate from host's group list, serve at `/llms.txt`
3. **`/.well-known/federation`** — static JSON, serve at the well-known path
4. **MCP server** — wrap the 5 read RPCs as MCP tools, serve at `/mcp`
5. **`robots.txt`** — explicitly allow AI crawlers

### Phase 2: SEO fundamentals (weeks 2-3)

1. **schema.org/Event JSON-LD** — render on every event page
2. **RSS/Atom/JSON Feed** — per-group and per-host feeds
3. **sitemap.xml** — events + groups sitemaps

### Phase 3: Federation discovery (weeks 3-4)

1. **Federation peer discovery** — crawl peers via `/.well-known/federation`
2. **Directory host** — reference implementation that aggregates host metadata
3. **Cross-host search** — directory-level search across all known hosts

### Phase 4: AI agent integrations (ongoing)

1. **Claude MCP integration** — publish the MCP server as a Claude-compatible connector
2. **ChatGPT integration** — publish as a ChatGPT plugin / GPT Action (uses OpenAPI spec)
3. **Perplexity integration** — ensure Perplexity's crawler can parse feeds + JSON-LD
4. **Apple Intelligence** — ensure Apple's event discovery surfaces can parse the structured data

---

## 7. What the protocol does not do

- **The protocol does not define a search engine.** Search (full-text, geographic, semantic) is a host-level or directory-level service. The protocol provides the data; search is built on top.
- **The protocol does not define a recommendation engine.** Recommendations ("you might like this group") are a host product feature, not a protocol feature.
- **The protocol does not require AI-native discovery.** A host can serve groups without exposing MCP, llms.txt, or OpenAPI. But a host that does not expose these is invisible to AI assistants, and in a market where AI discovery is replacing search discovery, invisibility is death.

---

## 8. Open questions

- **Federation-wide search protocol.** Should the protocol define a cross-host search RPC, or is search strictly a directory-level concern? The current design says directory-level, but a protocol-level search would make the federation more competitive with centralized platforms.
- **MCP authentication for write operations.** The MCP server exposes read tools without authentication (the protocol's read RPCs are unauthenticated). Write operations (RSVP, submit transition) require signed envelopes. Should the MCP server support write tools, or should writes always go through the ConnectRPC API directly?
- **Semantic search via embeddings.** Should hosts expose a semantic search endpoint (e.g., "find events similar to this one") using vector embeddings? This would let AI assistants do relevance-based discovery, not just keyword matching. Open question — requires embedding infrastructure on the host.
- **Rate limiting for AI agents.** The protocol's rate limiter targets steward transitions. AI agent read queries are not rate-limited at the protocol level. Hosts may want to implement per-IP or per-agent rate limiting on read endpoints to prevent abuse. This is a host policy decision, not a protocol decision.
- **Event deduplication across hosts.** If two hosts both serve the same group and both expose feeds, an AI agent will see the same event twice. The agent should deduplicate by `group_key + event_id`. This is a client-side concern, not a protocol concern, but it should be documented in the agent integration guide.