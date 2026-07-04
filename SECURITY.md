# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in federated-meetup:

1. **Do NOT open a public GitHub issue.**
2. Email: scott@scoble.me with details.
3. You will receive a response within 48 hours.
4. Once verified, a fix will be released and you will be credited (if desired).

## Security Measures

### Repo Hygiene
- No secrets, tokens, or credentials are committed to the repository
- GitHub Actions CI runs gosec (SAST) on every push and PR
- CI scans for common secret patterns (GitHub tokens, Stripe keys, AWS keys)
- Dependabot monitors Go modules, GitHub Actions, and Docker base images for CVEs
- `.gitignore` excludes `.env`, `*.db`, and other sensitive files

### Container Security
- Runs as non-root user (UID 65532)
- `readOnlyRootFilesystem: true` in k8s deployment
- All Linux capabilities dropped
- `allowPrivilegeEscalation: false`
- No CGO — fully static binary

### Network Security (k3s + Cloudflare Tunnel)
- No inbound ports required — Cloudflare Tunnel makes outbound connection only
- All public traffic terminates at Cloudflare's edge with TLS
- Cloudflare provides DDoS protection and WAF filtering
- Internal service is ClusterIP only (not exposed externally)

### Application Security
- HTTP Signature verification on ActivityPub inbox (permissive for v0)
- Stripe webhook signature verification
- Magic link RSVP tokens (no passwords stored)
- All transitions signature-verified in the protocol layer

### What You Should Do
- **Never commit secrets.** Use Kubernetes secrets, sealed-secrets, or external-secrets.
- **Rotate keys regularly.** Especially if you've ever pasted a token in a chat.
- **Enable branch protection** on the `main` branch.
- **Review Dependabot PRs** promptly.