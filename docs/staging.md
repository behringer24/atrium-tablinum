# Tunnel-Test und Staging

Ziel: den Server mit echten Clients (claude.ai, ChatGPT, Claude Code) und echtem Login prüfen, bevor er produktiv geht. Die automatischen Tests decken weder TEI mit bge-m3, noch Google/Entra, noch die Clients ab.

## 1. Tunnel-Test (lokal, Variante C)

1. Dev-Datenbank und TEI starten: `docker compose -f docker-compose.dev.yml up -d`. Beim ersten Start lädt TEI das Modell (einige Minuten); `curl localhost:8081/health` muss 200 liefern.
2. Tunnel auf `localhost:8080` einrichten (Cloudflare Tunnel, Tailscale Funnel oder ngrok). Ergebnis ist eine öffentliche HTTPS-Adresse.
3. `config.yaml`: `instance.base_url` = diese Adresse, `dev_mode: false`, ein echter Provider unter `auth.providers`, `bootstrap_admins` = eigene E-Mail. Beim Identity Provider die Redirect-URI `<base_url>/oauth/callback/<provider-id>` eintragen (siehe [auth.md](auth.md)).
4. `go run ./cmd/server -config config.yaml`; `curl <base_url>/healthz` und `<base_url>/.well-known/oauth-protected-resource` von außen prüfen.

Läuft der Tunnel auf demselben Host, `trusted_proxies: ["127.0.0.1"]` setzen (siehe [deployment.md](deployment.md), Host-Schutz).

## 2. Checkliste

| Prüfpunkt | Erwartung |
|---|---|
| MCP Inspector gegen `<base_url>/mcp` | OAuth-Flow läuft durch, Tool-Liste erscheint |
| claude.ai: Connector hinzufügen | Login beim Provider, danach Tools sichtbar |
| Claude Code: `claude mcp add --transport http atrium <base_url>/mcp` | Browser-Login über Loopback-Redirect |
| ChatGPT-Connector | Login, Tools sichtbar (Callback-URL vorher prüfen) |
| `store_knowledge` → `search_knowledge` | Treffer mit bge-m3, auch bei Synonymen |
| Nutzer mit fremder Domain | Login wird abgelehnt |
| Zweiter Nutzer | sieht den persönlichen Bereich des ersten nicht |
| Bootstrap-Admin | sieht `admin_*`-Tools; normaler Nutzer nicht |
| Token widerrufen / Nutzer deaktiviert | nächster Aufruf schlägt fehl |
| Lange Sitzung (> 1 h Token-Laufzeit) | Refresh funktioniert ohne neuen Login |
| `admin_health` | Datenbank und Embedder „ok“ |

Zusätzlich einmal prüfen, dass der HNSW-Index genutzt wird (`EXPLAIN` der Suchanfrage bei einigen tausend Chunks) und dass Entra mit einem echten Tenant funktioniert (bisher nur gegen einen Fake-Provider getestet).

## 3. Staging (Variante A oder B)

1. Eigene Subdomain, DNS auf den Host, Ports 80/443 offen.
2. [README](../README.md) Schnellstart durchführen; bei Variante A zuletzt `scripts/init-letsencrypt.sh`.
3. Checkliste aus Abschnitt 2 wiederholen, dazu:
   - Zertifikatserneuerung: `docker compose --profile proxy run --rm certbot renew --dry-run`
   - Neustart des Hosts: alle Dienste kommen von selbst hoch (`restart: unless-stopped`)
   - Backup und Wiederherstellung einmal gegen eine leere Datenbank durchspielen
4. Erst danach Produktivnutzer einladen.

## Bekannte offene Punkte

Client ID Metadata Documents, Reranker und Re-Embedding-Jobs sind nicht umgesetzt (Phase 2/3). Der Freigabe-Workflow ist nur als Grundfunktion da (Entwurf, Freigabe, Rückgabe, siehe [approval.md](approval.md)); ein Entwurf neben dem freigegebenen Stand und Benachrichtigungen fehlen noch.
