# Editions: Community and Pro

Atrium Tablinum comes in two editions.

## In short

- **Community** is free and complete for a small team: knowledge, skills, search, spaces, roles, review, the audit log, handoffs, personal spaces and offboarding basics. It is for **up to 8 active user accounts**. It never calls anybody and sends no telemetry.
- **Pro** is the same product for organisations that need more: more or unlimited users and extra features for scale, integration and governance. A licence file switches it on: a signed line of text that an instance admin installs with the tool `admin_license`, without a restart. It is checked offline and never calls anybody. Without a licence the Pro build runs as Community.
- **The official image** is meant to be the Pro build. It behaves as Community until a licence is installed, so you can try it, and upgrading changes nothing but the licence. (The pipeline that publishes it is not built yet; until then the image of this repository is the Community build.)
- **Security is never sold.** Roles, four-eyes review, risk classes, blocking, the audit log, login checks, rate limits, quotas and security updates are in both editions.
- Whatever the licence state, your content stays readable and exportable. The user limit only refuses **new accounts** (and reactivations), never existing ones, and never the administrators in `bootstrap_admins`.

**What counts as a user:** the registered accounts that are **active at the same time**. It is not the number of assistants connected (one user with Claude, ChatGPT and Claude Code is one user). **Locked accounts** (after `admin_offboard_user`) do not count, and accounts are never deleted, so offboarding frees the seat. Reactivating a locked account needs room again.

## What is in which edition

| Area | Community | Pro |
|---|---|---|
| Knowledge, knowledge types, skills, search, spaces, roles, review, audit log, handoffs, personal spaces | yes | yes |
| Users | up to 8 active accounts | more or unlimited, by licence |
| Licence management (`admin_license`) | | yes |
| Guided offboarding: handing a personal space on (`admin_transfer_personal_space`), reassigning responsibilities (`reassign_owner`) and the hints that lead through it | | yes (licence feature `offboarding`) |
| Further Pro features (group synchronisation, SCIM, audit export, review reminders, importers) | | planned, see the [roadmap](../ROADMAP.md) |

Each feature of the guides says which edition has it.

## Using a licence (Pro)

The licence is bound to the host of `instance.base_url` and carries a maintenance date: a build is covered **for ever** when the release line it was built from is dated on or before that day; newer release lines need a licence with a later date, and patch releases of a covered line are always covered. Without a covering licence the Pro features are off and the Community limit applies. Details for instance admins: [the instance admin guide](user/05-instance-admin.md#the-licence-pro-edition).

In the Pro build the `license:` section of `config.yaml` names an optional licence file (`file:`); a licence an instance admin installs at run time takes precedence.

## FAQ

**What if my team grows beyond 8 users?** That is what Pro is for. Get in touch with behringer24 for a licence.

**Which image do I pull?** The official one. It runs as Community until you install a licence.

**Does the Pro image phone home?** No. A licence is verified offline and nothing ever calls anybody.
