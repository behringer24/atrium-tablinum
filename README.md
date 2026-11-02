# Atrium Tablinum

Kurz **Atrium**: der Ort, an dem das Wissen eines Unternehmens zusammenkommt und bleibt. Im römischen Haus war das Atrium die Halle der Begegnung und das Tablinum der Raum für die Aufzeichnungen, offen zum Atrium und doch geordnet.

Selbst gehosteter, herstellerneutraler Wissensspeicher für KI-Assistenten (MCP). Wissen wird in Bereichen mit Rollen abgelegt, per Hybridsuche (Vektor + Volltext) gefunden und über OAuth 2.1 mit Google, Entra oder einem generischen OIDC-Provider abgesichert. Ein Projekt von behringer24.

## Schnellstart (Produktion)

1. `cp .env.example .env` und `cp config.example.yaml config.yaml`, beides ausfüllen
   (`instance.base_url`, `auth.providers`, `bootstrap_admins`, `POSTGRES_PASSWORD`).
2. Betriebsvariante wählen (Details und Proxy-Anforderungen in [docs/deployment.md](docs/deployment.md)):

| Variante | Ablauf |
|---|---|
| **A – mitgelieferter nginx + Let's Encrypt** | `DOMAIN` und `LETSENCRYPT_EMAIL` in `.env`, `instance.trusted_proxies: ["172.30.0.0/24"]`, dann `sh scripts/init-letsencrypt.sh` |
| **B – vorhandener Reverse Proxy** | `docker compose up -d`, `SERVER_BIND` und `trusted_proxies` passend setzen; Beispiele in `deploy/examples/` |
| **C – Tunnel/Plattform** | `docker compose up -d`, Tunnel zeigt auf `SERVER_BIND` |

3. Prüfen: `https://<domain>/healthz` und `/readyz`; Client-Einrichtung siehe [docs/auth.md](docs/auth.md).

Das erste Modell-Laden von `tei-embed` dauert einige Minuten; `/readyz` meldet bis dahin „nicht bereit“.

Releases und das fertige Image aus der GitHub Container Registry: [docs/release.md](docs/release.md).

Das Embedding-Backend ist wählbar: eigener TEI-Server, OpenAI und kompatible Dienste oder Amazon Bedrock. Einstellungen, Speicherbedarf und Datenschutz: [docs/embedding.md](docs/embedding.md).

Entwürfe, Freigabepflicht und die Prüfung durch Kuratoren: [docs/approval.md](docs/approval.md).

## Entwicklung

```bash
docker compose -f docker-compose.dev.yml up -d
ATRIUM_TEST_DATABASE_URL=postgres://knowledge:knowledge@localhost:5432/knowledge go test ./...
go run ./cmd/server -config config.yaml
```

## Backup

`scripts/backup.sh` erzeugt ein komprimiertes `pg_dump` und räumt alte Dumps auf. Täglich per Cron:

```
15 3 * * * cd /opt/atrium-tablinum && sh scripts/backup.sh
```

Wiederherstellen: `gunzip -c backups/atrium-tablinum-….sql.gz | docker compose exec -T postgres psql -U knowledge -d knowledge` in eine leere Datenbank. Embeddings sind im Dump enthalten; ein Modellwechsel erfordert dagegen eine Neuindizierung.

Das Dump enthält auch persönliche Bereiche. Ablage und Zugriff entsprechend schützen.

## Lizenz

Atrium Tablinum steht unter der [Elastic License 2.0](LICENSE): Quellcode einsehen, nutzen, ändern und weitergeben ist erlaubt. Nicht erlaubt sind das Anbieten als gehosteter oder verwalteter Dienst für Dritte sowie das Umgehen oder Entfernen von Lizenzschlüssel-Funktionen und Lizenzhinweisen. Das ist keine Open-Source-Lizenz im Sinne der OSI.
