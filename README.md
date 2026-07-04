# federated-meetup

A federation-first alternative to meetup.com. Open protocol, sovereign hosts,
AIEO-native discovery. No platform lock-in, no rent-seeking, no API keys.

**Status:** v0 — protocol working, web UI live, 250 tests passing, deployed at
[fm.scoble.me](https://fm.scoble.me).

## What is this?

Meetup.com extracts 30% of your group's revenue to be a centralized directory.
Federated Meetup replaces that with:

1. **An open protocol** — any host can run the server, any client can talk to it
   via ConnectRPC. No API keys, no rate limits, no platform tax.
2. **A host product** — a Go binary that serves the protocol API, a web UI
   (HTMX + SQLite), ticketing (Stripe), and MCP/AIEO discovery endpoints.
3. **Federation** — hosts talk to each other over a WireGuard mesh. Events on
   host A are discoverable from host B. No central server.

### Key features

- **Open ConnectRPC API** — 11 RPCs for groups, events, RSVPs, transitions
- **MCP server** — 6 tools + 4 discovery endpoints (`.well-known/federation`,
  `llms.txt`, `openapi.json`, `robots.txt`) so AI assistants can query any host
- **schema.org/Event JSON-LD** — every event page emits structured data for
  search engines and AI agents
- **Web UI** — Go + HTMX + SQLite, mobile-responsive, no JavaScript framework,
  no build step
- **Magic-link RSVP** — attendees RSVP with an email link, no accounts
- **Ticketing** — Stripe integration for paid events (mock checkout in v0)
- **Organizer dashboard** — revenue summary, attendee check-in, ticket management
- **Self-host parity** — the same binary runs the hosted instance and a
  self-hosted instance. No feature gating.
- **Deterministic simulator** — VOPR-shaped test harness (DDIL fault profiles,
  deterministic time/RNG, multi-host convergence)

## Quick start

### Prerequisites

- Go 1.25+
- That's it. SQLite is pure-Go (`modernc.org/sqlite`), no CGO.

### Build and run

```bash
git clone https://github.com/sscoble/federated-meetup.git
cd federated-meetup
go build ./...

# Generate a group key (any 32-byte hex string works for local dev)
export HOSTD_GROUP_KEY="0x$(python3 -c 'print("aa" * 32)')"
export HOSTD_NAME="my-host"
export HOSTD_BASE_URL="http://localhost:8091"
export HOSTD_DB_PATH="/tmp/fedmeetup.db"
export HOSTD_ADDR="127.0.0.1:8091"

go run ./cmd/fedmeetup
```

Open `http://localhost:8091` — you'll see the web UI with seeded demo data
(Vegas Programmers group + 3 events). The demo organizer token is
`demo-organizer-token`.

### Docker

```bash
docker build -t fedmeetup -f Dockerfile .
docker run -p 8091:8091 \
  -e HOSTD_GROUP_KEY="0x$(python3 -c 'print("aa" * 32)')" \
  -e HOSTD_NAME="my-host" \
  -e HOSTD_BASE_URL="http://localhost:8091" \
  fedmeetup
```

### Configuration

All config is via environment variables:

| Variable | Default | Description |
|---|---|---|
| `HOSTD_ADDR` | `:8080` | Listen address |
| `HOSTD_GROUP_KEY` | _(required)_ | Hex-encoded 32-byte Ed25519 public key |
| `HOSTD_NAME` | `hostd` | Canonical name this host advertises |
| `HOSTD_BASE_URL` | `http://localhost:8080` | Public base URL for absolute links/JSON-LD |
| `HOSTD_DB_PATH` | `fedmeetup.db` | SQLite path for web store |
| `HOSTD_DESCRIPTION` | `Federated Meetup host` | Host description for discovery |
| `HOSTD_AREA` | _(empty)_ | Geographic area for discovery (e.g. "Las Vegas, NV") |
| `HOSTD_THRESHOLD` | `0` | Initial threshold (v0: unused) |
| `HOSTD_STEWARDS` | _(empty)_ | Comma-separated hex keys (v0: unused) |
| `HOSTD_PEERS` | _(empty)_ | Comma-separated peer URLs to federate from (e.g. "http://peer:8080") |
| `HOSTD_SYNC_BOOTSTRAP` | `true` | Bootstrap group state from peers on startup |
| `HOSTD_SYNC_LIVE` | `true` | Maintain live Subscribe streams after bootstrap |

## Architecture

```
cmd/fedmeetup/          Host daemon — serves ConnectRPC + MCP + web UI on one port
cmd/fedmeetup-product/  Product daemon (standalone ConnectRPC product service)

proto/                  Open protobuf standard + ConnectRPC service definitions
                        (generated Go code is committed — no buf needed to build)

internal/
  crypto/               Ed25519 signing, multisig envelopes, X25519 key exchange
  types/                Protocol primitives (PublicKey, Hash, StateSnapshot)
  group/                Group state machine — Merkle KV, signed transitions
  hlc/                  Hybrid logical clocks for ordering
  host/                 ConnectRPC service implementation
  product/              Ticketing, orders, RSVPs, organizer auth
  mcp/                  MCP server + discovery endpoints
  web/                  Web UI — Go html/template + HTMX + SQLite
  payment/              Stripe webhook handling
  ratelimit/            Token bucket rate limiter
  federation/           Server-to-server sync client (bootstrap + live)

sim/                    Deterministic simulator (VOPR-shaped)
transport/wg/           Server-to-server WireGuard mesh transport
docs/                   Strategy + protocol + product docs (8 files, 2K+ lines)
```

### Two transports

1. **Client → Host**: Open protobuf + ConnectRPC. HTTP/2 over TLS. Anyone can
   implement a client.
2. **Host → Host**: WireGuard mesh. Private overlay, never public. Federation
   traffic doesn't cross the public internet in plaintext.

This split is what makes the federation actually federated — the federation
surface is not the same as the client attack surface.

## Build & test

```bash
# Build everything
go build ./...

# Run all tests with race detector
go test ./... -race -timeout 120s

# Run just the web tests
go test ./internal/web/... -timeout 60s

# Regenerate proto code (requires buf + protoc plugins)
./scripts/proto.sh
```

## Discovery (the moat)

Every host exposes:

- `/.well-known/federation` — host metadata (name, base URL, area, API endpoint)
- `/llms.txt` — LLM-readable site summary
- `/openapi.json` — OpenAPI spec for the REST surface
- `/robots.txt` — crawler directives
- `/mcp` — MCP server (6 tools: search events, get event, list groups, etc.)

This means AI assistants can discover and query any federated-meetup host
without API keys, registration, or a central directory. The discovery layer is
the commercial moat — it's what makes this AIEO-native rather than SEO-native.

## License

AGPL-3.0. See [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Docs

- [docs/01-PROBLEM.md](docs/01-PROBLEM.md) — The problem with meetup.com
- [docs/02-PROTOCOL.md](docs/02-PROTOCOL.md) — Protocol spec
- [docs/03-PRODUCT.md](docs/03-PRODUCT.md) — Product spec
- [docs/04-TARIFF.md](docs/04-TARIFF.md) — Pricing/tariff
- [docs/05-LAUNCH.md](docs/05-LAUNCH.md) — Launch plan
- [docs/06-OPEN-QUESTIONS.md](docs/06-OPEN-QUESTIONS.md) — Open questions
- [docs/07-AUDIT-FINDINGS.md](docs/07-AUDIT-FINDINGS.md) — Security audit
- [docs/08-DISCOVERY.md](docs/08-DISCOVERY.md) — Discovery/AIEO layer