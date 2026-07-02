# federated-meetup

A federation of sovereign meetup groups, with a host product on top.

The vision is in `docs/` (read `docs/02-PROTOCOL.md` first). The code is in
`internal/`, `sim/`, `proto/`, and `transport/`. **This README is about the
code.**

## Architecture

```
proto/                 — Open client protobuf standard. ConnectRPC service.
                         These .proto files ARE the canonical client-facing API.
                         Anything that wants to talk to a federated-meetup host
                         implements against them.

proto/federated_meetup/v1/*.pb.go
                       — Generated Go code from the .proto files. The
                         connectrpc service interface lives here.

internal/crypto/       — Ed25519 signing/verification with the protocol's
                         canonical message format and multisig envelope.

internal/types/        — In-memory Go representations of the protocol
                         primitives (PublicKey, Hash, StateSnapshot, etc.).

internal/group/        — The group state machine. Apply transitions, get
                         snapshots. State machine is a Merkle KV store;
                         transitions are signed; stewards evolve over time.

sim/                   — VOPR-shaped deterministic simulator. Hosts ask the
                         sim for time, not time.Now(). Same seed = same
                         scenario. DDIL fault profiles are a first-class
                         concern.

transport/             — Server-to-server federation transport. SEPARATE from
                         the client RPC. WireGuard-private; never public.

host/                  — (WIP) The HTTP host. ConnectRPC server. Wraps the
                         group state machine. Closed-source product features
                         live here.

client/                — (WIP) ConnectRPC client library. Open source.

cmd/                   — (WIP) fedmeetup (the host) and fedmeetup-sim (the CLI
                         simulator harness).
```

## Two transports, not one

This is the load-bearing architectural decision:

1. **Client → Host**: Open protobuf standard + ConnectRPC. Implementable by
   anyone. Reference client is open-source Go. Hosts expose this; clients
   consume it. HTTP/2 over TLS.

2. **Host → Host**: Private WireGuard mesh. NOT public. Federation traffic
   flows over a wireguard overlay between participating hosts. The overlay
   IP space is private to the federation; nothing crosses the public
   internet in plaintext, and the federation service is not addressable
   from outside the mesh.

This split is what makes the federation actually federated. If servers
talked to each other over the same RPC surface as clients, every host
would have a public attack surface equal to its federation surface. By
putting server-to-server over WireGuard, the federation traffic is
private by construction.

## VOPR shape

The simulator is designed the way TigerBeetle designs its tests:

- Deterministic time (no `time.Now()` in host code).
- Deterministic RNG (same seed → same scenario).
- DDIL fault profiles as first-class API (Denied/Disrupted/Intermittent/
  Limited).
- N hosts on a virtual mesh; the sim drives transitions, partitions,
  latency, drops, and asserts invariants.

A scenario = a seed + an initial setup + a sequence of actions + a set of
invariants. The sim replays scenarios and reports the first invariant
violation. **Same seed = same result, always.**

This is non-negotiable. The federation has to survive Vegas-to-Phoenix
flakiness in production; the simulator has to model that in test.

## Build & test

```bash
# Generate proto code (requires buf, protoc-gen-go, protoc-gen-connect-go,
# protoc-gen-validate — install via scripts/proto.sh or go install).
./scripts/proto.sh

# Build everything.
go build ./...

# Run tests (current smoke test: 4 hosts converge on a CREATE_GROUP).
go test ./...

# Run a specific scenario under the simulator harness.
go run ./cmd/fedmeetup-sim -seed=42 -hosts=4 -ddil=benign -scenario=create-group
```

## Status

2026-06-27: Scaffold up, VOPR simulator working, smoke test passes. Client
protobuf standard + ConnectRPC surface defined. DDIL profiles defined.
Server-to-server WireGuard mesh (real `golang.zx2c4.com/wireguard` userspace)
is the next milestone.

See `docs/02-PROTOCOL.md` for the protocol spec. See `docs/08-DISCOVERY.md`
for the SEO/AIEO/MCP discovery layer (the commercial moat). See
`docs/01-PROBLEM.md`, `03-PRODUCT.md`, `04-TARIFF.md`, `05-LAUNCH.md`,
`06-OPEN-QUESTIONS.md` for the rest of the strategy.

## Companion repos

- `sscoble/scobleclaw` — the agent harness this was designed inside
- `sscoble/posts` — a related decentralized messaging project
- `marcus/scobleclaw` — cross-cutting architecture decisions