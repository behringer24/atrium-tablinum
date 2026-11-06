# Editions: Community and Pro

Atrium Tablinum comes in two editions built from one code base. The reasons and the model are in the [design](design/licensing.md).

## In short

- **Community** is this repository, built as it is. It is source-available under the [Elastic License 2.0](../LICENSE), free to use, and complete for a small team: knowledge, skills, search, spaces, roles, review, the audit log, handoffs, personal spaces and offboarding basics. Its one hard limit is **8 active user accounts**, a constant of the code: there is no setting that lifts it. It has **no licence support, no licence keys and no telemetry**: it never calls anybody, and anyone can read the code to check.
- **Pro** is the same product with extra code that is not public and without the user limit of Community, switched on by a **licence file**: a signed line of text that an instance admin installs with the tool `admin_license`, without a restart. It is checked offline against public keys compiled into the Pro build and never calls anybody. Without a licence the Pro build runs with the Community limits.
- **The official image** is meant to be the Pro build, published publicly (the private pipeline that publishes it is not built yet; until then this repository's image is the Community build). It behaves as Community until a licence is installed, so you can try it, and upgrading changes nothing but the licence. If you want the Community build only, build it from this repository (`docker build .`).
- **Security is never sold.** Roles, four-eyes review, risk classes, blocking, the audit log, login checks, rate limits, quotas and security updates are in both editions. Pro adds scale, integration and governance extras.
- Nothing a licence does can lock you out of your content: whatever the licence state, stored content stays readable and exportable. A user limit only refuses **new accounts** (and reactivations), never existing ones, and never the administrators in `bootstrap_admins`.

**What counts as a user:** the registered accounts that are **active at the same time**. It is not the number of assistants connected (one user with Claude, ChatGPT and Claude Code is one user). **Locked accounts** (after `admin_offboard_user`) do not count, and accounts are never deleted, so offboarding frees the seat. Reactivating a locked account needs room again, so the limit cannot be passed by locking and unlocking.

## What is in which edition

| Area | Community | Pro |
|---|---|---|
| Knowledge, knowledge types, skills, search, spaces, roles, review, audit log, handoffs, personal spaces | yes | yes |
| Users | up to 8 active accounts | more or unlimited, by licence |
| Licence management (`admin_license`) | not part of this build | yes |
| Further Pro features (group synchronisation, SCIM, audit export, notifications, importers) | planned, see the [roadmap](../ROADMAP.md) | |

Each feature of the guides says which edition has it.

## Using a licence (Pro build)

The licence is bound to the host of `instance.base_url` and carries a maintenance date: a build is covered **for ever** when the release line it was built from is dated on or before that day; newer release lines need a licence with a later date, and patch releases of a covered line are always covered. Without a covering licence the Pro features are off and the limits are the Community ones. Details for instance admins: [the instance admin guide](user/05-instance-admin.md#the-licence).

In the Pro build the `license:` section of `config.yaml` names an optional licence file (`file:`); a licence an instance admin installs at run time takes precedence. The Community build refuses a `license:` section at start with a message saying that it has no licence support.

Issuing licences, the key ceremony and the register are the owner's business and live in the private Pro repository.

## FAQ

**Can I lift the user limit in the Community build?** No setting does it. The source is yours to modify under the Elastic License 2.0, but that licence does not allow you to circumvent licence key functionality or to offer the software as a managed service to third parties; if your team outgrows 8 accounts, that is what Pro is for.

**Which image do I pull?** The official one. It is the Pro build and runs as Community until you install a licence.

**Does the Pro image phone home?** No. A licence is verified offline and nothing ever calls anybody.
