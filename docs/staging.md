# Tunnel test and staging

Goal: check the server with real clients (claude.ai, ChatGPT, Claude Code) and a real login before it goes live. The automated tests cover neither TEI with bge-m3, nor Google/Entra, nor the clients.

## 1. Tunnel test (local, variant C)

1. Start the dev database and TEI: `docker compose -f docker-compose.dev.yml up -d`. On first start TEI downloads the model (a few minutes); `curl localhost:8081/health` must return 200.
2. Set up a tunnel to `localhost:8080` (Cloudflare Tunnel, Tailscale Funnel or ngrok). The result is a public HTTPS address.
3. `config.yaml`: `instance.base_url` = this address, `dev_mode: false`, a real provider under `auth.providers`, `bootstrap_admins` = your own e-mail. At the identity provider, register the redirect URI `<base_url>/oauth/callback/<provider-id>` (see [auth.md](auth.md)).
4. `go run ./cmd/server -config config.yaml`; check `curl <base_url>/healthz` and `<base_url>/.well-known/oauth-protected-resource` from outside.

If the tunnel runs on the same host, set `trusted_proxies: ["127.0.0.1"]` (see [deployment.md](deployment.md), host protection).

## 2. Checklist

| Check | Expectation |
|---|---|
| MCP Inspector against `<base_url>/mcp` | OAuth flow completes, tool list appears |
| claude.ai: add connector | Login at the provider, then tools visible |
| Claude Code: `claude mcp add --transport http atrium <base_url>/mcp` | Browser login via loopback redirect |
| ChatGPT connector | Login, tools visible (check the callback URL beforehand) |
| `store_knowledge` → `search_knowledge` | Hit with bge-m3, also for synonyms |
| User with a foreign domain | Login is rejected |
| Second user | does not see the first user's personal space |
| Bootstrap admin | sees `admin_*` tools; a normal user does not |
| Token revoked / user deactivated | next call fails |
| Long session (> 1 h token lifetime) | Refresh works without a new login |
| `admin_health` | Database and embedder "ok" |

In addition, check once that the HNSW index is used (`EXPLAIN` of the search query with a few thousand chunks) and that Entra works with a real tenant (so far only tested against a fake provider).

## 3. Staging (variant A or B)

1. Own subdomain, DNS pointing to the host, ports 80/443 open.
2. Go through the [README](../README.md) quick start; for variant A finish with `scripts/init-letsencrypt.sh`.
3. Repeat the checklist from section 2, plus:
   - Certificate renewal: `docker compose --profile proxy run --rm certbot renew --dry-run`
   - Reboot of the host: all services come up on their own (`restart: unless-stopped`)
   - Run through backup and restore once against an empty database
4. Only then invite production users.

## Known open points

Client ID Metadata Documents, reranker and re-embedding jobs are not implemented. The approval workflow covers drafts, curator review and returning entries (see [approval.md](approval.md)); a draft alongside the approved version and notifications are still missing. Planned work is listed in the [roadmap](../ROADMAP.md).
