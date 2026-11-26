# Installing Atrium

This guide is for the person who sets up an Atrium server for the first time: from an empty Linux host to a first login from Claude or ChatGPT. It takes about an hour, most of it waiting for downloads. The steps are the same for the Community and the Pro edition; the only difference is a licence that you install afterwards (see [Pro licence](#pro-licence)).

You need basic experience with Linux, Docker and DNS. You do not need to know Go or Postgres. After the installation, the [instance admin guide](05-instance-admin.md) takes over: spaces, users, context, backups and routines.

## Contents

- [What you are installing](#what-you-are-installing)
- [Before you start](#before-you-start)
- [Step 1: get the files](#step-1-get-the-files)
- [Step 2: choose how the server is reached](#step-2-choose-how-the-server-is-reached)
- [Step 3: set up the login](#step-3-set-up-the-login)
- [Step 4: write the configuration](#step-4-write-the-configuration)
- [Step 5: choose where the embeddings come from](#step-5-choose-where-the-embeddings-come-from)
- [Step 6: start](#step-6-start)
- [Step 7: check that it works](#step-7-check-that-it-works)
- [Step 8: first login and first steps](#step-8-first-login-and-first-steps)
- [Pro licence](#pro-licence)
- [Backups](#backups)
- [Updating](#updating)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)

## What you are installing

One Atrium instance serves exactly one organisation. A standard installation is four containers on one host:

| Container | Does | Notes |
|---|---|---|
| `server` | Atrium itself: the MCP endpoint, the login, all rules | Speaks plain HTTP on port 8080; TLS is done by a proxy in front |
| `postgres` | The database with the pgvector extension | Holds all content and the audit log |
| `tei-embed` | Turns text into vectors for the search | Optional: an API service can do this instead (step 5) |
| `db-init` | Creates the two database accounts, then exits | Runs at every start, changes nothing that is already right |

Optionally two more, the **bundled proxy** (`nginx` and `certbot`), which give you HTTPS with a free Let's Encrypt certificate if you have no proxy of your own.

Your users do not open a web page of Atrium. They use Atrium inside their AI assistant, which connects to `https://<your address>/mcp` and sends them to your identity provider (Google, Microsoft Entra or another OpenID Connect provider) to log in. There is no separate Atrium password.

## Before you start

**A host.** A Linux server, a virtual machine or a NAS with Docker. Sizing depends on the embedding service you choose in step 5:

| Embedding | RAM | CPU | Notes |
|---|---|---|---|
| Own TEI container, small-host settings | 4 GB | 2 cores | About 1.7 GB for TEI and a few seconds per saved section; fine for a team |
| Own TEI container, default settings | 16 GB | 4 to 8 cores | The default of the compose file needs about 12 GB for TEI alone |
| API service (OpenAI-compatible or Amazon Bedrock) | 2 GB | 1 to 2 cores | No TEI container; text is sent to the provider |

Disk: 10 GB is enough to begin with (the TEI model alone is about 2 GB). The TEI image exists for x86 only; on an ARM host use an API service.

**Software.** Docker Engine with the Compose plugin (`docker compose version` must work; Compose 2.24 or newer if you use an API service), Git, `curl` and `openssl`.

**A public address.** Assistants such as claude.ai and ChatGPT connect from the internet, so the instance needs a name like `atrium.example.com` that resolves to the host and a valid HTTPS certificate. A tunnel (Cloudflare Tunnel, Tailscale Funnel) works as well. A server that is reachable only inside your network cannot be used by those assistants; local clients such as Claude Code can use it.

**An identity provider.** Users log in with an account of your organisation. Google Workspace and private Google accounts work out of the box; Microsoft Entra and other OIDC providers need a few lines in `config.yaml` (step 3). You need the rights to register an application there.

**Names and an address for the first admin.** The e-mail address of the person who becomes the first instance admin, and the domain whose accounts may log in.

## Step 1: get the files

The compose file, the configuration examples and the scripts live in the repository. Use a release tag, not the development branch:

```bash
git clone https://github.com/behringer24/atrium-tablinum.git
cd atrium-tablinum
git checkout v0.7.1
```

(Replace `v0.7.1` with the newest release on the [releases page](https://github.com/behringer24/atrium-tablinum/releases).)

**Which image runs?** You have two choices:

- **Official image (recommended).** It is built and published for every release. In `docker-compose.yml`, in the service `server`, delete the line `build: .` and set `image:` to a fixed version, for example `image: ghcr.io/behringer24/atrium-tablinum:0.7.1`. A fixed version is better than `latest`, because an update then happens on purpose. The official image runs as Community until you install a Pro licence, so it is also the right choice if you plan to upgrade later.
- **Build it yourself (Community).** Leave the file as it is; `docker compose up -d` builds the image from the checkout. See [Releases](../release.md#build-from-source-community).

## Step 2: choose how the server is reached

The server speaks HTTP only. Decide who provides HTTPS:

| Variant | Choose it when | What you do |
|---|---|---|
| **A: bundled nginx and Let's Encrypt** | The host is dedicated to Atrium (or at least ports 80 and 443 are free) and has a public IP | Set `DOMAIN` and `LETSENCRYPT_EMAIL` in `.env`; in `config.yaml` set `instance.trusted_proxies: ["172.30.0.0/24"]`; start with `sh scripts/init-letsencrypt.sh` (step 6) |
| **B: your own reverse proxy** | nginx, Traefik, Caddy or HAProxy already runs on the host | Point the proxy to the server (`SERVER_BIND`, default `127.0.0.1:8080`); set `instance.trusted_proxies` to the proxy's address; start with `docker compose up -d` |
| **C: tunnel or platform** | Cloudflare Tunnel, Tailscale Funnel or a platform that terminates TLS | Point the tunnel to `SERVER_BIND`; set `trusted_proxies` to the tunnel's address (for a connector on the same host, `["127.0.0.1"]`); start with `docker compose up -d` |

**For variant A**, create the DNS record first (an `A` record, and `AAAA` if the host has IPv6) and open ports 80 and 443 in the firewall, otherwise the certificate cannot be issued.

**For variants B and C**, the proxy has to meet a few requirements that MCP and the login need: forward every path unchanged (`/mcp`, `/oauth/*`, `/.well-known/*`, `/export/*`), no buffering of responses, long read timeouts (an hour), the headers `Host`, `X-Forwarded-For` and `X-Forwarded-Proto`, and a request size of at least 2 MB. The list with ready-made examples (nginx, Traefik, Caddy) is in [Deployment](../deployment.md#requirements-for-every-proxy). Only publish the server port on an interface the proxy can reach; never on a public one.

**`trusted_proxies` matters.** It names the proxies whose `X-Forwarded-*` headers the server believes. Without it every user behind your proxy appears to come from one address and shares one allowance for logins and registrations, and the protection against requests with a foreign host name works differently. Set it to the address of your proxy, not to a whole network you do not control.

## Step 3: set up the login

Atrium never sees a password. It asks your identity provider who the user is and lets them in if their account is allowed. Register Atrium once at the provider. The address to enter as the redirect (callback) address is always

```
<your public address>/oauth/callback/<provider id>
```

for example `https://atrium.example.com/oauth/callback/google`. The provider id is the `id` in `config.yaml`; the shipped configuration uses `google`.

### Google (the default)

1. Open the [Google Cloud Console](https://console.cloud.google.com/), create or choose a project, and open **APIs & Services**, then **Credentials** (in newer versions called **Google Auth Platform**).
2. Configure the **consent screen** if you have not done so. Choose **Internal** if your organisation uses Google Workspace: then only accounts of your Workspace can log in. Choose **External** for private Google accounts; an external app in the status "Testing" admits only the test users you list, so publish it before the rollout. The basic scopes (`openid`, `email`, `profile`) are enough.
3. Create credentials: **OAuth client ID**, application type **Web application**.
4. Under **Authorized redirect URIs** enter `https://<your address>/oauth/callback/google`.
5. Copy the **client ID** and the **client secret** into `.env` (step 4) as `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`.

The menu names change from time to time; what you need is a web application client with that one redirect address.

Accounts are admitted if their Google Workspace domain is the one you set as `ATRIUM_ALLOWED_DOMAIN`. For private Gmail accounts, which have no domain, list the addresses under `allowed_emails` in `config.yaml` (see the comment in `config.example.yaml`).

### Microsoft Entra ID or another OIDC provider

The shipped `config.yaml` has a commented example for each. Remove the comment characters, fill in client id, client secret and, for Entra, the tenant id (`allowed_tenants`); for a generic provider the `issuer` and the allowed domains. Register `https://<your address>/oauth/callback/<id>` as the redirect address at the provider. Note: Entra does not tell whether an e-mail address was verified, so the first admin cannot log in through Entra unless you also set `email_trusted: true` for a tenant without guests, or log in through a provider that verifies. Details for every provider, and why, are in [Login and OAuth](../auth.md).

You can add more domains, addresses or tenants later without a restart (`admin_allowlist`, see the [instance admin guide](05-instance-admin.md)).

## Step 4: write the configuration

Two files, both in the folder of the checkout:

```bash
cp .env.example .env
cp config.example.yaml config.yaml
```

For a basic setup you only fill in `.env`. `config.yaml` stays as it is, except for `trusted_proxies` (step 2) and further login providers (step 3).

Generate the passwords with `openssl rand -hex 24` (three different ones; only letters and digits, because they end up in a URL):

| Variable | What to enter |
|---|---|
| `POSTGRES_PASSWORD` | A new strong password for the database superuser. Applies only when the database is created; changing it later does not change it in the database |
| `APP_DB_PASSWORD` | A new strong password for the account the server runs as (cannot change the schema or the audit log) |
| `MIGRATE_DB_PASSWORD` | A new strong password for the account that applies updates of the database |
| `ATRIUM_BASE_URL` | The public HTTPS address, with no trailing slash: `https://atrium.example.com` |
| `ATRIUM_ALLOWED_DOMAIN` | The e-mail domain whose accounts may log in: `example.com` |
| `ADMIN_EMAIL` | The e-mail address of the first instance admin. Must be the address the person logs in with |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | From step 3 |
| `ATRIUM_SHORT_NAME`, `ATRIUM_ORGANIZATION_NAME` | Optional, read on the first start only. How users say it in chat (default "Atrium") and the name of your organisation. An instance admin changes them later with `admin_instance`. Set a distinct short name if one assistant connects to several Atrium instances |
| `DOMAIN`, `LETSENCRYPT_EMAIL` | Variant A only |
| `SERVER_BIND` | Variants B and C only, if the proxy cannot reach `127.0.0.1:8080` |
| `TEI_ARGS`, `EMBEDDING_API_KEY` | Step 5 |

Protect the file: it holds secrets. `chmod 600 .env`. It is not part of the repository and must never be committed or sent around.

## Step 5: choose where the embeddings come from

Search combines full text with vectors. A backend produces the vectors; without a working one, search still works by full text but finds fewer synonyms and paraphrases. Pick one per instance:

| Choice | Data leaves your server | What to do |
|---|---|---|
| **Own TEI container** (default, model bge-m3) | No | On a host with less than 12 GB RAM set in `.env`: `TEI_ARGS=--model-id BAAI/bge-m3 --max-batch-tokens 1024 --auto-truncate`. The first start downloads about 2 GB; x86 only |
| **OpenAI or a compatible service** | Yes, to the provider | Set the backend in `config.yaml` (`embedding:` section, example in the file), put the key in `.env` as `EMBEDDING_API_KEY`, and start with `docker compose -f docker-compose.yml -f docker-compose.api.yml up -d`. Choose it before the first entry is saved |
| **Amazon Bedrock** | Yes, within your own AWS account and region | As above; access through the IAM role of the host or the `AWS_*` variables in `.env` |

The vector length (`embedding.dimensions`) is fixed per database. Choose the backend before you store anything; changing it later means re-embedding everything. Full settings, memory figures, privacy notes and how to change the model: [Embedding](../embedding.md).

## Step 6: start

**Variant A (bundled proxy):**

```bash
sh scripts/init-letsencrypt.sh
```

The script creates a temporary certificate so that nginx can start, requests the real certificate from Let's Encrypt, and starts the whole stack. It needs the DNS record and open ports from step 2.

**Variants B and C:**

```bash
docker compose up -d
```

If you chose an API service, use the command from step 5 instead.

The first start takes a few minutes: images are downloaded, the database is created, `db-init` sets up the two accounts, the server applies the database migrations, and TEI downloads and loads its model. Follow it with:

```bash
docker compose logs -f server tei-embed
```

The server logs a warning if it is started as the database superuser; with the compose file in this repository that does not happen.

## Step 7: check that it works

```bash
docker compose ps
curl -fsS https://atrium.example.com/healthz
curl -fsS https://atrium.example.com/readyz
```

- `docker compose ps` shows `server`, `postgres` and `tei-embed` running and `db-init` exited with code 0.
- `/healthz` answers as soon as the server runs.
- `/readyz` answers `ok` when the database and the embedding service are ready. Until TEI has loaded its model it says `not ready`; that is normal for the first minutes. If it stays that way, the reason is in the server log and, once you can log in, in `admin_health`.
- `https://atrium.example.com/.well-known/oauth-protected-resource` should return a short JSON document: proof that the proxy forwards the paths the login needs.

Do this from outside the network (a phone with mobile data does), so that you test what the assistants will see.

## Step 8: first login and first steps

**Connect an assistant.** Add the Atrium address `https://atrium.example.com/mcp` as a custom connector (Claude, ChatGPT: in the settings for connectors or apps) or MCP server (Claude Code: `claude mcp add --transport http atrium https://atrium.example.com/mcp`). Log in with the account of `ADMIN_EMAIL`. At the first connection of an assistant Atrium shows a consent page with the name of the application and your account; check it and choose **Allow**. [Getting started](00-getting-started.md#1-connect-your-assistant) shows the same steps from the user's side.

**Check.** Ask: *"Which Atrium spaces exist, and what is my role?"* and then *"Show the Atrium health."* (`admin_health`). Database and embedding service should be "ok". If the `admin_*` tools do not appear, the account you logged in with is not the one in `ADMIN_EMAIL`.

**Then go on with the instance admin guide**, section [Setting up a new instance](05-instance-admin.md#setting-up-a-new-instance): set the context that tells assistants what Atrium is for, create the first spaces, appoint curators, run a small pilot and schedule backups. Tell your people beforehand that the first connection of every assistant asks for consent.

Two more things worth doing on day one: write down who may restore the server and where `.env` and the backups are kept, and read [Incident runbook](../incident.md) once, so that you know what to do before you need it.

## Pro licence

The official image runs as Community (up to 8 active accounts) until you install a licence; nothing has to be reinstalled. Send behringer24 the address your instance runs under (`ATRIUM_BASE_URL`), because a licence is issued for that host. Then tell your assistant: *"Install this licence: atrium-license-v1.…"* (`admin_license`, instance admin). No restart is needed. Everything about licences, the user limit and the Pro features is in [Atrium Pro](../pro/pro-edition.md); the editions are listed in [Editions](../licensing.md).

## Backups

Before you invite users, set up a backup and test the restore once.

```bash
sh scripts/backup.sh
```

writes a compressed database dump to `./backups` and deletes dumps older than 14 days. Schedule it daily, for example with cron:

```
15 3 * * * cd /opt/atrium-tablinum && sh scripts/backup.sh
```

The dump holds everything, personal spaces included. Store it on another machine, encrypt the medium and restrict access. Keep `.env` safe as well (it is not in the dump). To restore, start a fresh database and load the dump into the empty database:

```bash
gunzip -c backups/atrium-tablinum-<date>.sql.gz | docker compose exec -T postgres psql -U knowledge -d knowledge
```

Details and the readable export of all knowledge: [Operating the system](05-instance-admin.md#operating-the-system).

## Updating

1. Read the **Upgrade notes** in the [changelog](../../CHANGELOG.md) for every version between yours and the new one. Some updates need a new setting.
2. Take a backup (`sh scripts/backup.sh`).
3. Get the new files: `git fetch --tags && git checkout vX.Y.Z`. If you keep your own edits of `docker-compose.yml`, merge them.
4. Set the new version in the `image:` line, then `docker compose pull && docker compose up -d`. Use the new image together with the new compose file.
5. Check `/readyz` and `admin_health`.

Migrations only go forward: going back to an older version after a migration needs the backup. Rolling back within the same schema means pointing `image:` to the earlier version. More in [Releases](../release.md).

## Troubleshooting

| Symptom | Likely cause and fix |
|---|---|
| `docker compose` stops with "set APP_DB_PASSWORD in .env" | A password is missing in `.env`. Fill in all three passwords |
| `/readyz` says `not ready` for a long time | TEI is still downloading or loading its model (look at `docker compose logs tei-embed`), or has too little memory: use the small-host `TEI_ARGS`. For the exact reason see the server log or `admin_health` |
| TEI is killed or restarts | Out of memory. Set `TEI_ARGS` as in step 5 or use an API service |
| The login ends with an error at Google | The redirect address at Google differs from `<ATRIUM_BASE_URL>/oauth/callback/google` in even one character (scheme, host, path), or the consent screen is in the status "Testing" and the account is not a listed test user |
| "Account not allowed" after login | The address is outside `ATRIUM_ALLOWED_DOMAIN` and not in `allowed_emails`. An instance admin can add it with `admin_allowlist`; the first admin must use `ADMIN_EMAIL` |
| Everything works but the assistant cannot connect | The proxy does not forward `/mcp`, `/oauth/*` or `/.well-known/*`, buffers responses, or the base address in `.env` is not the public one. Test the `.well-known` address from outside |
| Many users see "too many requests" | `trusted_proxies` is empty or wrong, so everybody shares the proxy's address |
| Variant A: the certificate is not issued | DNS does not point to the host yet, or port 80 is blocked. Fix it and run `sh scripts/init-letsencrypt.sh` again |
| The server starts but saving fails with a model message | TEI serves another model than `embedding.model` names. Align them; see [Embedding](../embedding.md) |
| After `docker compose down -v` everything is gone | `-v` deletes the volumes, that is, the database. Never use it on a production instance |

Collect the facts for a support request with *"Collect the Atrium support information."* (`admin_support_bundle`). It contains no content, no names and no secrets; read it before you pass it on.

## FAQ

**Do I need a licence to try Atrium?**
No. The Community edition needs nothing and is meant for up to 8 active accounts. A Pro licence changes only the licence, not the installation.

**Can I run Atrium without a public address?**
Only for clients that run on your own machines, such as Claude Code, through a local address. claude.ai and ChatGPT connect from the internet and need a public HTTPS address.

**Can I try it on my laptop first?**
Yes. [Tunnel test and staging](../staging.md) describes a local run with a temporary tunnel and a checklist of what to test with real clients before you go live.

**Can I use my own database server?**
Yes, if it has the pgvector extension. Create the two accounts once with `scripts/db-roles.sql` and set `database.url` and `database.migration_url`; the steps are in [Deployment](../deployment.md#separate-database-accounts).

**Can several organisations share one instance?**
No. One instance serves exactly one organisation; run a second instance for another one.

**Where are the settings I did not see here?**
In `config.yaml`: every setting has a comment in `config.example.yaml`. Configuration changes need a restart (`docker compose up -d`); allowed logins, spaces, members, context and limits change at runtime through the `admin_*` tools.

**Which ports does the host need open?**
For variant A, 80 and 443. For variants B and C, only what your proxy or tunnel needs; the Atrium server port (8080) and the database stay internal.

**How do I know the instance is healthy later?**
Let your monitoring call `/readyz`, and ask *"Show the Atrium health."* now and then. Keep an eye on the expiry of the certificate at your proxy: Atrium does not check it.

**How do I stop or start it?**
`docker compose stop` and `docker compose up -d`. The data lives in volumes and survives; `docker compose down` removes containers but keeps the volumes.
