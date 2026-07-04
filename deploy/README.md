# Self-Hosting Federated Meetup

This guide walks you through deploying federated-meetup on your own
server using Docker Compose and Caddy for automatic HTTPS.

## Prerequisites

- A Linux server with Docker and Docker Compose installed.
- A domain name pointed at your server (A/AAAA DNS records).
- Ports 80 and 443 open for Caddy (HTTP for ACME challenges, HTTPS for traffic).

## Quick Start

### 1. Clone the repo

```bash
git clone https://github.com/sscoble/federated-meetup.git
cd federated-meetup
```

### 2. Configure environment variables

```bash
cp .env.example .env
```

Edit `.env`:

| Variable | Required | Description |
|----------|----------|-------------|
| `HOSTD_GROUP_KEY` | **Yes** | Hex-encoded 32-byte Ed25519 public key. Generate: `openssl rand -hex 32` |
| `DOMAIN` | Yes (for TLS) | Your domain name, e.g. `meetup.example.com` |
| `HOSTD_NAME` | No | Canonical host name (default: `hostd`) |
| `HOSTD_BASE_URL` | No | Public HTTPS URL (default: `https://$DOMAIN`) |
| `HOSTD_DB_PATH` | No | SQLite path for web store (default: `/data/fedmeetup.db`) |
| `HOSTD_PROTOCOL_DB` | No | SQLite path for protocol log (default: `/data/protocol.db`). Set empty for in-memory. |
| `HOSTD_DESCRIPTION` | No | Host description for MCP discovery |
| `HOSTD_AREA` | No | Geographic area (e.g. "Las Vegas, NV") |
| `HOSTD_PEERS` | No | Comma-separated peer URLs for federation sync |
| `STRIPE_SECRET_KEY` | No | Stripe key for ticketing. Unset = mock payments. |

### 3. Configure the domain in Caddyfile

The default `Caddyfile` uses the `DOMAIN` environment variable.
Caddy reads `{$DOMAIN}` from its environment. For docker-compose,
the domain is passed via the `.env` file.

If you want a fixed domain, edit `Caddyfile` and replace `{$DOMAIN:localhost}`
with your domain:

```
meetup.example.com {
    reverse_proxy fedmeetup:8091
}
```

### 4. Start the services

```bash
docker compose up -d
```

Caddy will automatically provision TLS certificates from Let's Encrypt
on first request. Visit `https://your-domain` to verify.

### 5. Check logs

```bash
# All services
docker compose logs -f

# Just fedmeetup
docker compose logs -f fedmeetup

# Just Caddy
docker compose logs -f caddy
```

On startup with `HOSTD_PROTOCOL_DB` set, you'll see:

```
fedmeetup: replayed N transitions from /data/protocol.db, root=0x...
```

### 6. Verify health

```bash
curl https://your-domain/healthz
# => ok

curl https://your-domain/identity
# => {"group_key":"0x...","name":"hostd","threshold":0}
```

## Persistence

The protocol state machine persists all signed transitions to a
SQLite append-only log (`HOSTD_PROTOCOL_DB`). On restart, all
transitions are replayed and the exact same state root is
reconstructed.

- **Web store** (`HOSTD_DB_PATH`): RSVPs, sessions, event cache.
- **Protocol log** (`HOSTD_PROTOCOL_DB`): Signed transition log.
  If empty, protocol state is in-memory only (lost on restart).

Both databases live in the `fedmeetup-data` Docker volume by default.

## Production Deployment

For production, use `deploy/docker-compose.prod.yml`:

```bash
docker compose -f deploy/docker-compose.prod.yml up -d
```

The production variant:
- Uses explicit TLS via Caddy with ACME email configured.
- Sets restart policies to `always`.
- Mounts Caddy data for certificate persistence.

## Federation

To sync from a peer host, set `HOSTD_PEERS`:

```env
HOSTD_PEERS=https://peer1.example.com,https://peer2.example.com
HOSTD_SYNC_BOOTSTRAP=true
HOSTD_SYNC_LIVE=true
```

The syncer fetches the peer's transition log and replays it locally.
Every transition is signature-verified — a malicious peer can only
omit transitions, not inject fake ones.

## Updating

```bash
git pull
docker compose build
docker compose up -d
```

The protocol log is replayed from disk on restart — no data loss.

## Backup

The `fedmeetup-data` volume contains both SQLite databases:

```bash
docker run --rm -v fedmeetup-data:/data -v $(pwd):/backup alpine \
  tar czf /backup/fedmeetup-data.tar.gz /data
```

Restore:

```bash
docker run --rm -v fedmeetup-data:/data -v $(pwd):/backup alpine \
  tar xzf /backup/fedmeetup-data.tar.gz -C /
```