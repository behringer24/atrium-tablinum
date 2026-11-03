# Deployment and reverse proxy

The Go server only speaks HTTP (`instance.listen`, default `:8080`). TLS termination is **not part of the server** and is solved differently depending on the environment. The bundled nginx + certbot is therefore optional.

## Operating variants

| Variant | Who terminates TLS | Compose call |
|---|---|---|
| **A – Bundled** (server without existing infrastructure) | nginx + certbot from this repo | `docker compose --profile proxy up -d` |
| **B – Own reverse proxy** (nginx, Traefik, Caddy, HAProxy, Ingress …) | existing proxy | `docker compose up -d` (without profile) |
| **C – Tunnel/platform** (Cloudflare Tunnel, Tailscale Funnel, PaaS) | tunnel or platform | `docker compose up -d`, tunnel separately |

Common rules for all variants:

- `instance.base_url` is always the **public HTTPS address** that Claude/ChatGPT see.
- `instance.trusted_proxies` contains the IPs/CIDRs of the proxies whose `X-Forwarded-*` headers are trusted. Empty = headers are ignored.
- The server port is published on the host via `SERVER_BIND` (default `127.0.0.1:8080`). Variant A needs no publishing; for B set it to the interface the proxy can reach.

## Host protection of the MCP SDK

The SDK rejects requests that arrive via loopback but carry a foreign `Host` header (protection against DNS rebinding). A reverse proxy on the same host that forwards to `127.0.0.1:8080` would be blocked by this. The server therefore switches the protection off as soon as `instance.trusted_proxies` is set. Anyone using a local proxy must therefore always enter `trusted_proxies` (e.g. `["127.0.0.1"]`).

## Requirements for every proxy

For MCP (Streamable HTTP/SSE) and OAuth to work:

1. Forward **all paths** to the server, in particular `/mcp`, `/oauth/*` and `/.well-known/oauth-*` (they must reach the server on the public HTTPS domain – do not redirect, do not cache).
2. **No buffering** of responses (SSE): nginx `proxy_buffering off`, Traefik/Caddy: the default is fine, with Caddy possibly `flush_interval -1`.
3. **Long read timeouts** (≥ 1 h) for open streams.
4. Set `Host`, `X-Forwarded-For`, `X-Forwarded-Proto`.
5. Request size at least `policy.max_document_bytes` + overhead (e.g. 2 MB).
6. HTTP/1.1 to the upstream, do not hard-code the `Connection` header to `upgrade`.

### nginx (existing instance, variant B)

Snippet for the `server` block of the domain; TLS/certificates stay as in the existing installation:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;      # or host/container of the server
    proxy_http_version 1.1;
    proxy_set_header Host              $host;
    proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header Connection        "";

    proxy_buffering    off;
    proxy_cache        off;
    proxy_read_timeout 3600s;
    client_max_body_size 2m;
}
```

More ready-made examples: `deploy/examples/traefik-labels.yml` and `deploy/examples/Caddyfile`.

## Implementation decisions

- The Compose network has a fixed subnet (`172.30.0.0/24`) so that `trusted_proxies` is stable for the bundled nginx.
- `docker-compose.yml` contains `server`, `postgres`, `tei-embed` without a profile; `nginx` and `certbot` are under `profiles: ["proxy"]`.
- The server image has **no** dependency on nginx/certbot; `/readyz` and `admin_health` work in all variants. The certificate expiry check in `admin_health` is skipped if no bundled proxy is running (the certificate is then outside our control).
- `scripts/init-letsencrypt.sh` is only needed for variant A.
- The nginx configuration in the repo is a template (`deploy/nginx/knowledge.conf.template`) with `${DOMAIN}`, so no file has to be edited by hand.
