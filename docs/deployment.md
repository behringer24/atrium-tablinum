# Deployment und Reverse Proxy

Der Go-Server spricht nur HTTP (`instance.listen`, Standard `:8080`). TLS-Terminierung ist **nicht Teil des Servers** und je nach Umgebung unterschiedlich gelöst. Der mitgelieferte nginx + certbot ist deshalb optional.

## Betriebsvarianten

| Variante | Wer terminiert TLS | Compose-Aufruf |
|---|---|---|
| **A – Bundled** (Server ohne vorhandene Infrastruktur) | nginx + certbot aus diesem Repo | `docker compose --profile proxy up -d` |
| **B – Eigener Reverse Proxy** (nginx, Traefik, Caddy, HAProxy, Ingress …) | vorhandener Proxy | `docker compose up -d` (ohne Profil) |
| **C – Tunnel/Plattform** (Cloudflare Tunnel, Tailscale Funnel, PaaS) | Tunnel bzw. Plattform | `docker compose up -d`, Tunnel separat |

Gemeinsame Regeln für alle Varianten:

- `instance.base_url` ist immer die **öffentliche HTTPS-Adresse**, die Claude/ChatGPT sehen.
- `instance.trusted_proxies` enthält die IPs/CIDRs der Proxys, deren `X-Forwarded-*`-Header vertraut werden. Leer = Header werden ignoriert.
- Der Server-Port wird per `SERVER_BIND` auf dem Host veröffentlicht (Standard `127.0.0.1:8080`). Variante A braucht keine Veröffentlichung; bei B auf die Schnittstelle setzen, die der Proxy erreicht.

## Host-Schutz des MCP-SDK

Das SDK weist Anfragen ab, die über Loopback ankommen, aber einen fremden `Host`-Header tragen (Schutz gegen DNS-Rebinding). Ein Reverse Proxy auf demselben Host, der an `127.0.0.1:8080` weiterleitet, würde dadurch blockiert. Der Server schaltet den Schutz deshalb ab, sobald `instance.trusted_proxies` gesetzt ist. Wer einen lokalen Proxy nutzt, muss `trusted_proxies` also immer eintragen (z. B. `["127.0.0.1"]`).

## Anforderungen an jeden Proxy

Damit MCP (Streamable HTTP/SSE) und OAuth funktionieren:

1. **Alle Pfade** an den Server weiterreichen, insbesondere `/mcp`, `/oauth/*` und `/.well-known/oauth-*` (müssen auf der öffentlichen HTTPS-Domain beim Server landen – nicht umleiten, nicht cachen).
2. **Keine Pufferung** von Antworten (SSE): nginx `proxy_buffering off`, Traefik/Caddy: Standard passt, bei Caddy ggf. `flush_interval -1`.
3. **Lange Lese-Timeouts** (≥ 1 h) für offene Streams.
4. `Host`, `X-Forwarded-For`, `X-Forwarded-Proto` setzen.
5. Request-Größe mindestens `policy.max_document_bytes` + Overhead (z. B. 2 MB).
6. HTTP/1.1 zum Upstream, `Connection`-Header nicht fest auf `upgrade` setzen.

### nginx (vorhandene Instanz, Variante B)

Snippet für den `server`-Block der Domain; TLS/Zertifikate bleiben wie bei der bestehenden Installation:

```nginx
location / {
    proxy_pass http://127.0.0.1:8080;      # bzw. Host/Container des Servers
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

Weitere fertige Beispiele: `deploy/examples/traefik-labels.yml` und `deploy/examples/Caddyfile`.

## Umsetzungsentscheidungen

- Das Compose-Netz hat ein festes Subnetz (`172.30.0.0/24`), damit `trusted_proxies` für den mitgelieferten nginx stabil ist.
- `docker-compose.yml` enthält `server`, `postgres`, `tei-embed` ohne Profil; `nginx` und `certbot` stehen unter `profiles: ["proxy"]`.
- Das Server-Image enthält **keine** Abhängigkeit zu nginx/certbot; `/readyz` und `admin_health` funktionieren in allen Varianten. Die Zertifikatsablauf-Prüfung in `admin_health` entfällt, wenn kein Bundled-Proxy läuft (Zertifikat liegt dann außerhalb unserer Kontrolle).
- `scripts/init-letsencrypt.sh` wird nur für Variante A benötigt.
- Nginx-Konfiguration im Repo liegt als Template (`deploy/nginx/knowledge.conf.template`) mit `${DOMAIN}`, damit keine Datei von Hand editiert werden muss.
