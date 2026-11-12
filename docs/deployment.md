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

## Limits against abuse

The server itself limits: requests are read within a minute (headers within ten seconds, `IdleTimeout` two minutes, headers at most 64 KB), so slow bodies cannot hold connections forever, and open streams are not affected. `/oauth/register` allows 20 requests a minute per address, the login steps 60, `/oauth/token` and `/oauth/revoke` 300, and clients that never completed a login are deleted an hour after they registered (only those count toward the cap of 5000 registrations). Reads and writes of logged-in users are limited per user. All of these keys are the client address, so `instance.trusted_proxies` must be right: without it every client behind your proxy shares the proxy's address and its allowance.

Your own proxy should add what it does best: a connection and request limit for `/oauth/` (nginx `limit_req_zone` and `limit_conn`), `client_body_timeout` and `client_header_timeout`, and HSTS. The bundled nginx template sets HSTS. `/readyz` is public and says only `ok` or `not ready`; if you do not want it reachable from outside, allow it for your monitoring only in the proxy.

## Separate database accounts

The server must not run as the database's superuser: a flaw in it, or anybody who gets hold of its connection string, could then switch off the triggers that freeze released skills, rewrite or delete audit entries, or change the schema. Atrium therefore uses two accounts:

| Account | Does | Cannot |
|---|---|---|
| `atrium_app` (the server, `database.url`) | Read and write the data, append to the audit log | Change the schema, create or drop tables, switch triggers off, alter or delete an audit entry, read the migration list |
| `atrium_migrate` (`database.migration_url`) | Owns the schema and runs the migrations at start | Nothing is run with it besides the migrations |

**With the compose setup nothing is left to do.** The service `db-init` runs `scripts/db-roles.sql` as the superuser before the server starts, creates the accounts with the passwords `APP_DB_PASSWORD` and `MIGRATE_DB_PASSWORD` from `.env`, and exits; the server gets `DATABASE_URL` (the first account) and `DATABASE_MIGRATION_URL` (the second) from the same file. The script changes nothing that is already right, so it is safe on every start. A new installation starts hardened from its first start. The superuser's password (`POSTGRES_PASSWORD`) is used only by `postgres`, `db-init` and the backup.

**Updating an installation that ran as the superuser:** add `APP_DB_PASSWORD` and `MIGRATE_DB_PASSWORD` to `.env` (compose refuses to start without them; use `openssl rand -hex 24`, letters and digits only) and run `docker compose up -d`. `db-init` converts the existing database; the data is not touched. Take a backup first, as before any update. Do not point `config.yaml` at the superuser again: `DATABASE_URL` and `DATABASE_MIGRATION_URL` from the environment are what the server uses, unless `database.url` is written out in your `config.yaml`.

**Without compose**, set up the accounts once, as a superuser, on the database (take a backup first; the script is safe to run again, also on an empty database):

```bash
psql -U <superuser> -d <database> -v ON_ERROR_STOP=1   -v app_password='...' -v migrate_password='...' < scripts/db-roles.sql
```

Then set `database.url` to `postgres://atrium_app:<app password>@<host>:5432/<database>` and `database.migration_url` (or the environment variable `DATABASE_MIGRATION_URL`) to `postgres://atrium_migrate:<migrate password>@<host>:5432/<database>`, and restart. At start the migrations run with the second account and the server then continues with the first. Tables, sequences and functions that later migrations create are usable by the server without further steps. Keep `atrium_migrate`'s password only where the server starts, and keep the superuser's out of the server's environment. The server logs a warning at start while it still runs as a superuser.

The test suite can run the integration tests with the restricted account (`ATRIUM_TEST_RESTRICTED=1 go test -p 1 ./...`), to check that no code path needs more. The one test that tampers with the schema on purpose (switching triggers off) fails in that mode, which is the point.

## Icon

Clients show an icon next to the server. Atrium serves one without login at `/favicon.ico` and `/icon` and also names it in the server info (`icons`), so clients that read either find it. The built-in Atrium icon is used unless `instance.icon_file` points to an image (svg, png, ico, jpg or webp, up to 256 KB). Which of the two sources a client uses is up to the client; many only fetch the favicon of the domain. For that, the proxy must forward `/favicon.ico` to Atrium and not answer it itself, otherwise the favicon of the proxy's default site is shown.

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
