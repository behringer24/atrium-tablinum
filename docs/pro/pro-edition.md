# Atrium Pro: licence and guided offboarding

This guide is for the **instance admins** (and, for one section, the space admins) of an organisation that runs the **Pro** edition. It covers what the Community edition does not have: installing and renewing a licence, and the guided offboarding. Everything else works as described in the [instance admin guide](../user/05-instance-admin.md) and the other guides. Which edition has what is listed in [Editions](../licensing.md).

The Pro edition is the official image. Until a licence is installed it behaves exactly like Community (up to 8 active accounts), so you can install the image first and add the licence when you have it.

## Contents

- [The licence](#the-licence)
- [Looking at the licence](#looking-at-the-licence)
- [Installing, renewing and removing a licence](#installing-renewing-and-removing-a-licence)
- [Updating Atrium with a licence](#updating-atrium-with-a-licence)
- [The user limit](#the-user-limit)
- [Guided offboarding](#guided-offboarding)
- [FAQ](#faq)

## The licence

A licence is one line of text that starts with `atrium-license-v1.`. You receive it from behringer24.

- **It is checked on your server, offline.** Atrium never calls anybody to check it, and nothing about your instance is sent anywhere.
- **It belongs to your instance.** It is issued for the host of your `instance.base_url`, so tell us the address you run under. A licence installed on an instance with another address is refused.
- **It lists what it switches on:** the number of users (`users`; 0 means unlimited) and features such as `offboarding`.
- **It carries a maintenance date** (`maintenance_until`) and, for a trial or subscription, an optional expiry date (`expires`).

**Maintenance, not an expiry.** Atrium releases come in *release lines* (0.4, 0.5, 1.0 and so on); `server_info` and `admin_license` name the line of the running build and the day it was released. A licence covers every build of a line released on or before its maintenance date, **for ever**: you can run what you have indefinitely, and patch releases of a covered line are always covered. A *newer* line needs a licence with a later maintenance date. You renew once, in the running instance, usually once a year.

**Nothing is ever blocked.** In every state other than `valid` the instance runs as Community. Content stays readable and exportable, and no account is locked.

## Looking at the licence

*"Show the Atrium licence."* (`admin_license action=get`, instance admin) reports:

| Field | Meaning |
|---|---|
| `status` | `none` (no licence, Community), `valid`, `not covered` (the running build is of a newer line than the maintenance covers; the text names the dates), `expired` (the `expires` date has passed), `wrong domain` (the licence is for another address), `invalid` (unreadable or not issued by behringer24) |
| `edition`, `licensee`, `features`, `limits`, `maintenance_until`, `expires` | What the licence says |
| `source` | `installed` (by an admin at run time) or `file` (named in the configuration) |
| `notice` | A reminder with the date, see below |
| `build` | Version, release line and its release date, for comparison with `maintenance_until` |
| `users` | The active accounts now, the limit in force (0 = unlimited) and `over_limit` |
| `warning` | Only when the active accounts have reached the limit |

**Reminders.** From 60 days before the end of maintenance, and before the `expires` date of a trial or subscription, the notice appears in the licence report, in `admin_health` (which never turns unhealthy because of it) and as a warning in the server log at start. After the end of maintenance it stays as a reminder that newer release lines need a renewed licence; the running build is not affected.

## Installing, renewing and removing a licence

Say: *"Install this licence: atrium-license-v1.…"* (`admin_license action=install`). Your assistant tells you what the licence contains and asks for your confirmation, because limits and features change at once. No restart is needed.

- The installation is **refused** if the licence is unreadable, not issued by behringer24 or for another address.
- It is **accepted** if it does not cover the running build, so that the report can say why it has no effect (`not covered`).
- A licence you install at run time is stored in the database and takes precedence over a file.
- **Alternatively** an operator can name a licence file in `config.yaml` (section `license:` with the key `file:`, the path of the licence file); it is read at start.

**Renewal** is installing the new line once the maintenance has been extended. The reminder tells you 60 days ahead. Anyone who does not want new releases never has to touch the file again.

**Removing.** *"Remove the installed licence."* (`action=remove`, after confirmation) deletes the installed licence; one named in the configuration stays. The question before the removal tells you how many accounts are active and what the Community limit means for them. After a removal the instance runs as Community: **nobody is locked out**, see [the user limit](#the-user-limit).

Installing and removing are recorded in the audit log (`license_install`, `license_remove`) with licence id, licensee, edition and maintenance date, never the licence text.

## Updating Atrium with a licence

Read the [changelog](../../CHANGELOG.md) and update to the version you want as usual. If after an update the report says **`not covered`**, the new build is of a line newer than your maintenance. Pro features are off and the Community limits apply, all data stays accessible. Either go back to the covered version, or renew the maintenance and install the new licence.

## The user limit

With a licence that sets a limit, that limit applies; without one, Community's 8 active accounts. What counts as a user is explained in [Editions](../licensing.md): registered accounts that are active at the same time, not connected assistants, and locked accounts do not count.

If more accounts are active than the limit in force (after a removal, an expiry, or an update beyond the maintenance), everybody keeps working. The licence report shows `over_limit` and a warning, and only **new accounts and reactivations** wait until there is room or a covering licence is installed again.

## Guided offboarding

The Pro feature `offboarding` leads through what happens after somebody leaves. `admin_offboard_user` works as in every edition (it locks the account, ends the sessions, deletes the handoffs and reports the personal space and its deletion date); with the feature, its answer also counts the entries that name the person as responsible (`responsible_for`) and names the next steps. The steps themselves:

### Handing a personal space on (instance admin)

*"Transfer the personal space of anna@example.com."* (`admin_transfer_personal_space`, after confirmation). It works only for **locked** accounts. Two modes:

- **`mode=space`** turns the personal space into a **team space** (you give slug, name and description and the members, for example the supervisor as `admin` and the successor as `curator`). Entries keep their IDs and version history. The successor needs `curator` because authors can only change and move their own entries. The members review, move or delete; afterwards archive the space with `admin_archive_space`. **Skills the owner had released alone are blocked** at this point, because nobody reviewed them: a space admin lifts a block (`unblock_skill`) and a curator releases the version again. Like every team space, it is visible to instance admins.
- **`mode=merge`** moves all entries into the **personal space of a successor** (`target_email`), who then owns them like their own. Their quota applies; if it does not suffice, nothing is moved. You cannot merge into your own space (four-eyes principle). **Skills are not carried over**: they stay in the old personal space and are deleted with it. Use `mode=space` if skills must survive.

The transfer is audited with account, mode and count, never with titles or contents. Carry it out against the employee's will only when documented, preferably with a second person, and agree the procedure with your data protection officer and, where applicable, the works council.

If nothing is transferred, the personal space is deleted after `spaces.personal.retention_days_after_offboarding` days (default 30), as in [Offboarding](../offboarding.md).

### Handing on responsibilities (space admin, instance admin)

Entries that name the person as responsible keep the name, marked `former`, until somebody takes over. One call hands them on: *"Make tom@example.com responsible for everything anna@example.com was responsible for."* (`reassign_owner`). The first call only reports how many entries are concerned; after your confirmation the second one does it.

- Space admins act on the spaces they administer, instance admins on all team spaces. You can limit the call to one space.
- The new person must be active and able to read the space. Entries in spaces they cannot read are left as they are and reported.
- No new versions are written; the audit log records `owner_reassign`.

## FAQ

**Do I need a licence to try the official image?** No. It runs as Community until you install one.

**The report says `wrong domain`. What now?** The licence is for another address than your `instance.base_url`. Tell behringer24 the address you run under and install the new licence.

**The report says `not covered` after an update.** See [Updating Atrium with a licence](#updating-atrium-with-a-licence).

**I removed the licence and have more users than Community allows. Are they locked out?** No. Every active account keeps working; only new accounts and reactivations are refused until there is room.

**Does Atrium phone home to check the licence?** No. The check is offline, and neither edition sends telemetry.

**Do I have to restart after installing a licence?** No.

**Who sees the licence?** Only instance admins, through `admin_license`. Other users see the edition and the release line in `server_info`.

**The tools `admin_license`, `admin_transfer_personal_space` and `reassign_owner` do not appear.** They exist only in the Pro edition. `admin_license` and `admin_transfer_personal_space` are for instance admins, `reassign_owner` for space admins and instance admins. Without the feature `offboarding` the two offboarding tools refuse with a message.
