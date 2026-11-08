# Editions: Community and Pro

Atrium Tablinum comes in two editions.

## In short

- **Community** is free and complete for a small team: knowledge, skills, search, spaces, roles, review, the audit log, handoffs, personal spaces and offboarding basics. It is for **up to 8 active user accounts**. It never calls anybody and sends no telemetry.
- **Pro** is the same product for organisations that need more: more or unlimited users and extra features for scale, integration and governance. For a licence, get in touch with behringer24.
- **The official image** (`ghcr.io/behringer24/atrium-tablinum`) is the one to run. It works as Community, so you can try it, and an upgrade to Pro changes nothing but the licence. If you prefer to build the Community edition yourself, see [Build from source](release.md#build-from-source-community).
- **Security is never sold.** Roles, four-eyes review, risk classes, blocking, the audit log, login checks, rate limits, quotas and security updates are in both editions.
- Whatever the edition, your content stays readable and exportable. The user limit only refuses **new accounts** (and reactivations), never existing ones.

**What counts as a user:** the registered accounts that are **active at the same time**. It is not the number of assistants connected (one user with Claude, ChatGPT and Claude Code is one user). **Locked accounts** (after `admin_offboard_user`) do not count, and accounts are never deleted, so offboarding frees the seat. Reactivating a locked account needs room again.

## What is in which edition

| Area | Community | Pro |
|---|---|---|
| Knowledge, knowledge types, skills, search, spaces, roles, review, audit log, handoffs, personal spaces | yes | yes |
| Users | up to 8 active accounts | more or unlimited |
| Guided offboarding (handing a personal space on, reassigning responsibilities) | | yes |
| Further features for scale, integration and governance | | planned, see the [roadmap](../ROADMAP.md) |

Each feature of the guides says which edition has it. How a Pro licence is installed and what the guided offboarding does: [Atrium Pro](pro/pro-edition.md).

## FAQ

**What if my team grows beyond 8 users?** That is what Pro is for. Get in touch with behringer24.

**Which image do I pull?** The official one.

**Does Atrium phone home?** No. Neither edition calls anybody or sends telemetry.
