# Contributing to federated-meetup

Thanks for your interest! This is an early-stage project — things move fast and
break. Here's how to get started.

## Prerequisites

- Go 1.25+
- No CGO required (SQLite is pure-Go via `modernc.org/sqlite`)
- For proto regeneration only: `buf`, `protoc-gen-go`, `protoc-gen-connect-go`

## Getting started

```bash
git clone https://github.com/sscoble/federated-meetup.git
cd federated-meetup
go build ./...
go test ./... -race -timeout 120s
```

If all tests pass, you're ready to hack.

## Running locally

```bash
export HOSTD_GROUP_KEY="0x$(python3 -c 'print("aa" * 32)')"
export HOSTD_NAME="my-host"
export HOSTD_BASE_URL="http://localhost:8091"
export HOSTD_DB_PATH="/tmp/fedmeetup.db"
export HOSTD_ADDR="127.0.0.1:8091"

go run ./cmd/fedmeetup
```

Open `http://localhost:8091`. Demo organizer token: `demo-organizer-token`.

## Code organization

- `internal/web/` — Web UI (Go html/template + HTMX + SQLite)
- `internal/host/` — ConnectRPC service implementation
- `internal/product/` — Ticketing, orders, RSVPs, organizer auth
- `internal/group/` — Group state machine (Merkle KV, signed transitions)
- `internal/mcp/` — MCP server + discovery endpoints
- `sim/` — Deterministic simulator
- `proto/` — Protobuf definitions + generated Go code (committed)
- `docs/` — Strategy and protocol docs (do not modify without discussion)

## Testing

All code must have tests. The project uses Go's standard testing package.

```bash
# Run everything with race detector
go test ./... -race -timeout 120s

# Run a specific package
go test ./internal/web/... -v -timeout 60s

# Run a specific test
go test ./internal/web/... -run TestEventPage -v
```

The simulator (`sim/`) is a VOPR-shaped deterministic test harness. If you're
adding protocol-level features, add a sim scenario.

## Proto changes

Generated Go code is committed to the repo so that `go build` works without
buf installed. If you change `.proto` files:

```bash
./scripts/proto.sh   # regenerates .pb.go files
```

Then commit the generated files alongside your `.proto` changes.

## Security

- All HTML is auto-escaped via `html/template`
- CSP headers on all responses (`default-src 'self'`)
- CSRF protection on all POST forms (double-submit cookie pattern)
- Magic-link auth only — no passwords, no OAuth
- Input validation on all form fields

If you find a security issue, please email scott@scoble.me instead of opening
a public issue.

## License

By contributing, you agree that your contributions are licensed under AGPL-3.0.