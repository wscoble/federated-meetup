# Self-Hosting Federated Meetup

This guide covers three deployment paths:

1. **[Docker Compose](#docker-compose)** — simplest, good for a single VPS
2. **[K3s + Cloudflare Tunnel](#k3s--cloudflare-tunnel)** — recommended for homelab / on-prem
3. **[Federation](#federation)** — connecting multiple hosts together

---

## Docker Compose

### Prerequisites

- A Linux server with Docker and Docker Compose installed
- A domain name pointed at your server (A/AAAA DNS records)
- Ports 80 and 443 open for Caddy (HTTP for ACME challenges, HTTPS for traffic)

### Quick Start

```bash
git clone https://github.com/wscoble/federated-meetup.git
cd federated-meetup
cp .env.example .env
# Edit .env — set HOSTD_GROUP_KEY and DOMAIN
docker compose up -d
```

Caddy will automatically provision TLS certificates from Let's Encrypt.

### Environment Variables

| Variable | Required | Description |
|----------|----------|-------------|
| `HOSTD_GROUP_KEY` | **Yes** | Hex-encoded 32-byte Ed25519 public key. `openssl rand -hex 32` |
| `DOMAIN` | Yes (for TLS) | Your domain name |
| `HOSTD_NAME` | No | Canonical host name (default: `hostd`) |
| `HOSTD_BASE_URL` | No | Public HTTPS URL (default: `https://$DOMAIN`) |
| `HOSTD_DB_PATH` | No | SQLite path for web store (default: `/data/fedmeetup.db`) |
| `HOSTD_PROTOCOL_DB` | No | SQLite path for protocol log (default: `/data/protocol.db`) |
| `HOSTD_DESCRIPTION` | No | Host description for MCP discovery |
| `HOSTD_AREA` | No | Geographic area (e.g. "Las Vegas, NV") |
| `HOSTD_PEERS` | No | Comma-separated peer URLs for federation sync |
| `STRIPE_SECRET_KEY` | No | Stripe key for ticketing. Unset = mock payments. |
| `HOSTD_SMTP_HOST` | No | SMTP server hostname |
| `HOSTD_SMTP_PORT` | No | SMTP server port |
| `HOSTD_SMTP_USER` | No | SMTP username |
| `HOSTD_SMTP_PASS` | No | SMTP password |
| `HOSTD_SMTP_FROM` | No | From email address |

---

## K3s + Cloudflare Tunnel

This is the recommended deployment for a homelab or on-prem server. The
Cloudflare Tunnel means **no inbound ports need to be opened** — the tunnel
makes an outbound connection to Cloudflare's edge.

### Architecture

```
Internet → Cloudflare Edge → Tunnel → k3s cluster → fedmeetup service
                                              ↑
                                              └── Cloudflare Tunnel pod
                                                  (outbound connection)
```

### Prerequisites

1. **k3s installed** on your server:
   ```bash
   curl -sfL https://get.k3s.io | sh -
   ```

2. **Cloudflare account** with a domain managed by Cloudflare DNS.

3. **Cloudflare Zero Trust** access (free tier is fine):
   - https://one.dash.cloudflare.com/

4. **kubectl** configured to talk to your k3s cluster:
   ```bash
   export KUBECONFIG=/etc/rancher/k3s/k3s.yaml
   ```

### Step 1: Create the namespace and PVC

```bash
kubectl apply -f deploy/k8s/namespace.yaml
kubectl apply -f deploy/k8s/pvc.yaml
```

### Step 2: Create secrets

**Application secrets:**
```bash
kubectl -n fedmeetup create secret generic fedmeetup-secrets \
  --from-literal=HOSTD_GROUP_KEY="0x$(openssl rand -hex 32)" \
  --from-literal=STRIPE_SECRET_KEY="" \
  --from-literal=HOSTD_SMTP_HOST="" \
  --from-literal=HOSTD_SMTP_PORT="" \
  --from-literal=HOSTD_SMTP_USER="" \
  --from-literal=HOSTD_SMTP_PASS="" \
  --from-literal=HOSTD_SMTP_FROM=""
```

**Cloudflare tunnel token:**

1. Go to https://one.dash.cloudflare.com/ → Networks → Tunnels → Create tunnel
2. Name it `fedmeetup`
3. Copy the tunnel token
4. Create the secret:
   ```bash
   kubectl -n fedmeetup create secret generic cloudflare-tunnel-token \
     --from-literal=TOKEN='eyJhZxxxxxxx...'
   ```

### Step 3: Configure the tunnel public hostname

In the Cloudflare dashboard for your tunnel:

| Setting | Value |
|---------|-------|
| Subdomain | `fm` |
| Domain | `scoble.me` |
| Path | (leave empty) |
| Service type | `HTTP` |
| URL | `fedmeetup.fedmeetup.svc.cluster.local:8080` |

### Step 4: Edit the ConfigMap

Edit `deploy/k8s/configmap.yaml` and set your domain and preferences:

```yaml
data:
  HOSTD_BASE_URL: "https://fm.scoble.me"  # your public URL
  HOSTD_AREA: "Las Vegas, NV"             # your area
```

### Step 5: Apply manifests and deploy

```bash
kubectl apply -f deploy/k8s/configmap.yaml
kubectl apply -f deploy/k8s/deployment.yaml
kubectl apply -f deploy/k8s/service.yaml
kubectl apply -f deploy/k8s/cloudflare-tunnel.yaml
```

Or apply everything at once (excluding secrets, which are managed out-of-band):

```bash
kubectl apply -k deploy/k8s/
```

### Step 6: Verify

```bash
# Check pods are running
kubectl -n fedmeetup get pods

# Check health
kubectl -n fedmeetup port-forward svc/fedmeetup 8080:8080
curl http://localhost:8080/healthz
# => ok

# Check the tunnel
kubectl -n fedmeetup logs deployment/cloudflare-tunnel

# Test from the internet
curl https://fm.scoble.me/healthz
# => ok
```

### Updating

When a new image is published to GHCR:

```bash
kubectl -n fedmeetup set image deployment/fedmeetup \
  fedmeetup=ghcr.io/wscoble/federated-meetup:latest
kubectl -n fedmeetup rollout status deployment/fedmeetup
```

Or trigger the deploy workflow from GitHub Actions (requires `K3S_KUBECONFIG` secret).

### Backup

```bash
# Back up the PVC data
kubectl -n fedmeetup exec deployment/fedmeetup -- \
  tar czf /tmp/backup.tar.gz /data
kubectl -n fedmeetup cp deployment/fedmeetup:/tmp/backup.tar.gz ./backup.tar.gz
```

---

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

---

## CI/CD

Three GitHub Actions workflows are included:

| Workflow | File | Trigger | Description |
|----------|------|---------|-------------|
| CI | `.github/workflows/ci.yml` | push/PR to main | Build, test, vet, gosec security scan, secret scan |
| Docker Publish | `.github/workflows/docker-publish.yml` | push to main, tags | Build multi-arch image, push to GHCR |
| Deploy | `.github/workflows/deploy.yml` | after Docker Publish, or manual | Apply manifests to k3s, rollout, health check |

### Required GitHub Secrets for Deploy

| Secret | Description |
|--------|-------------|
| `K3S_KUBECONFIG` | Base64-encoded kubeconfig for your k3s cluster |

Generate:
```bash
cat /etc/rancher/k3s/k3s.yaml | base64 -w 0
# Add as a GitHub repo secret
```