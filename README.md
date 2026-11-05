# Atrium Tablinum

Short: **Atrium**, the place where a company's knowledge comes together and stays. In a Roman house the atrium was the hall of meeting and the tablinum the room for the records, open to the atrium and yet ordered.

A self-hosted, vendor-neutral knowledge store for AI assistants (MCP). Knowledge is kept in spaces with roles, found through hybrid search (vector + full text) and secured with OAuth 2.1 using Google, Entra or a generic OIDC provider. A project of behringer24.

## Quick start (production)

1. `cp .env.example .env` and `cp config.example.yaml config.yaml`, fill in both
   (`instance.base_url`, `auth.providers`, `bootstrap_admins`, `POSTGRES_PASSWORD`).
2. Choose a deployment variant (details and proxy requirements in [docs/deployment.md](docs/deployment.md)):

| Variant | Steps |
|---|---|
| **A – bundled nginx + Let's Encrypt** | `DOMAIN` and `LETSENCRYPT_EMAIL` in `.env`, `instance.trusted_proxies: ["172.30.0.0/24"]`, then `sh scripts/init-letsencrypt.sh` |
| **B – existing reverse proxy** | `docker compose up -d`, set `SERVER_BIND` and `trusted_proxies` accordingly; examples in `deploy/examples/` |
| **C – tunnel/platform** | `docker compose up -d`, the tunnel points to `SERVER_BIND` |

3. Verify: `https://<domain>/healthz` and `/readyz`; for client setup see [docs/auth.md](docs/auth.md).

The first model load of `tei-embed` takes a few minutes; until then `/readyz` reports "not ready".

Releases and the prebuilt image from the GitHub Container Registry: [docs/release.md](docs/release.md).

Editions and licences (Community, Pro, offline-verified licence files, how a licence is issued and installed): [docs/licensing.md](docs/licensing.md), design in [docs/design/licensing.md](docs/design/licensing.md).

The embedding backend is selectable: your own TEI server, OpenAI and compatible services, or Amazon Bedrock. Settings, memory requirements and data protection: [docs/embedding.md](docs/embedding.md).

**Why Atrium, and user guides** for every group of users (everyday use, authors, curators, space admins, instance admins), with FAQs and as PDF: start with [docs/user/00-why-atrium.md](docs/user/00-why-atrium.md), index in [docs/user/README.md](docs/user/README.md).

Drafts, mandatory approval and review by curators: [docs/approval.md](docs/approval.md).

Company-wide skills (approved working instructions for assistants) with their own review and immutable versions: [docs/skills.md](docs/skills.md). Two skills for filing sources and checking spaces ship with Atrium, to be installed and reviewed by each organisation: [skills/README.md](skills/README.md).

## Development

```bash
docker compose -f docker-compose.dev.yml up -d
ATRIUM_TEST_DATABASE_URL=postgres://knowledge:knowledge@localhost:5432/knowledge go test ./...
go run ./cmd/server -config config.yaml
```

The data model after the latest migration (all tables with columns, keys, constraints, indexes, triggers, relationships and what each table is for) is an interactive page: [docs/data-model.html](docs/data-model.html). Open it in a browser from a checkout; GitHub shows HTML files as source only. It needs no network access. A migration that changes the schema updates the page in the same change; `node tools/docs/check-data-model.mjs` compares the page with the migrations and names every difference.

## Backup

`scripts/backup.sh` creates a compressed `pg_dump` and removes old dumps. Daily via cron:

```
15 3 * * * cd /opt/atrium-tablinum && sh scripts/backup.sh
```

Restore: `gunzip -c backups/atrium-tablinum-….sql.gz | docker compose exec -T postgres psql -U knowledge -d knowledge` into an empty database. Embeddings are included in the dump; a model change, however, requires reindexing.

The dump also contains personal spaces. Protect its storage and access accordingly.

## License

Atrium Tablinum is licensed under the [Elastic License 2.0](LICENSE): you may view, use, modify and redistribute the source code. Not permitted are offering it as a hosted or managed service to third parties, and circumventing or removing license key functionality and license notices. This is not an open source license in the sense of the OSI.
