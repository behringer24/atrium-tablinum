# Atrium Pro: licence, guided offboarding, content separation and spaces in a hierarchy

This guide is for the **instance admins** (and, for two sections, the space admins) of an organisation that runs the **Pro** edition. It covers what the Community edition does not have: installing and renewing a licence, the guided offboarding, the separation of content from operations and spaces below other spaces. Everything else works as described in the [instance admin guide](../user/05-instance-admin.md) and the other guides. Which edition has what is listed in [Editions](../licensing.md).

The Pro edition is the official image. Until a licence is installed it behaves exactly like Community (up to 8 active accounts), so you can install the image first and add the licence when you have it.

## Contents

- [The licence](#the-licence)
- [Looking at the licence](#looking-at-the-licence)
- [Installing, renewing and removing a licence](#installing-renewing-and-removing-a-licence)
- [Updating Atrium with a licence](#updating-atrium-with-a-licence)
- [The user limit](#the-user-limit)
- [Guided offboarding](#guided-offboarding)
- [Separating content from operations](#separating-content-from-operations)
- [Spaces below other spaces](#spaces-below-other-spaces)
- [FAQ](#faq)

## The licence

A licence is one line of text that starts with `atrium-license-v1.`. You receive it from behringer24.

- **It is checked on your server, offline.** Atrium never calls anybody to check it, and nothing about your instance is sent anywhere.
- **It belongs to your instance.** It is issued for the host of your `instance.base_url`, so tell us the address you run under. A licence installed on an instance with another address is refused.
- **It lists what it switches on:** the number of users (`users`; 0 means unlimited) and features such as `offboarding`, `content_separation` and `space_hierarchy`.
- **It is for one major version and runs for a term.** It names the major version it covers (`major`) and, for a subscription or a trial, the day it ends (`expires`). Licences issued earlier carry a maintenance date (`maintenance_until`) instead of a major version; they keep working as described at the end of this section.

**One major version, one term.** Atrium versions are numbered `major.minor.patch` (1.4.2); `server_info` and `admin_license` name the version and release line of the running build. A licence for major version 1 covers every build of 1.x, so each minor release and patch of that version is included while the licence runs. A build of a *newer* major version (2.0 and later) needs a licence for that major version; a licence for a newer major version also covers the earlier ones, so you can go back. When the term ends, the instance runs as Community until a new licence is installed. Renewing brings the licence for the major version that is current then.

*Licences with a maintenance date.* An older licence covers every build of a release line released on or before its `maintenance_until`, for ever; a newer line needs a licence with a later date.

**Nothing is ever blocked.** In every state other than `valid` the instance runs as Community. Content stays readable and exportable, and no account is locked. One thing differs: spaces that were arranged below each other keep their arrangement and their rights, see [Spaces below other spaces](#spaces-below-other-spaces).

## Looking at the licence

*"Show the Atrium licence."* (`admin_license action=get`, instance admin) reports:

| Field | Meaning |
|---|---|
| `status` | `none` (no licence, Community), `valid`, `not covered` (the running build is of a newer major version than the licence covers, or, for a licence with a maintenance date, of a newer line; the text names which), `expired` (the `expires` date has passed), `wrong domain` (the licence is for another address), `invalid` (unreadable or not issued by behringer24) |
| `edition`, `licensee`, `features`, `limits`, `major`, `maintenance_until`, `expires` | What the licence says |
| `source` | `installed` (by an admin at run time) or `file` (named in the configuration) |
| `notice` | A reminder with the date, see below |
| `build` | Version, major version, release line and its release date |
| `users` | The active accounts now, the limit in force (0 = unlimited) and `over_limit` |
| `warning` | Only when the active accounts have reached the limit |

**Reminders.** From 60 days before the `expires` date, the notice appears in the licence report, in `admin_health` (which never turns unhealthy because of it) and as a warning in the server log at start. For a licence with a maintenance date it also names the end of maintenance, and afterwards stays as a reminder that newer release lines need a renewed licence; the running build is not affected.

## Installing, renewing and removing a licence

Say: *"Install this licence: atrium-license-v1.…"* (`admin_license action=install`). Your assistant tells you what the licence contains and asks for your confirmation, because limits and features change at once. No restart is needed.

- The installation is **refused** if the licence is unreadable, not issued by behringer24 or for another address.
- It is **accepted** if it does not cover the running build, so that the report can say why it has no effect (`not covered`).
- A licence you install at run time is stored in the database and takes precedence over a file.
- **Alternatively** an operator can name a licence file in `config.yaml` (section `license:` with the key `file:`, the path of the licence file); it is read at start.

**Renewal** is installing the new licence before the old one ends. The reminder tells you 60 days ahead, and a renewal made in time continues the running term.

**Removing.** *"Remove the installed licence."* (`action=remove`, after confirmation) deletes the installed licence; one named in the configuration stays. The question before the removal tells you how many accounts are active and what the Community limit means for them. After a removal the instance runs as Community: **nobody is locked out**, see [the user limit](#the-user-limit).

Installing and removing are recorded in the audit log (`license_install`, `license_remove`) with licence id, licensee, edition, major version and end date, never the licence text.

## Updating Atrium with a licence

Read the [changelog](../../CHANGELOG.md) and update to the version you want as usual. If after an update the report says **`not covered`**, the new build is of a newer major version than your licence covers (or, for a licence with a maintenance date, of a newer line). Pro features are off and the Community limits apply, all data stays accessible. Either go back to the covered version, or get a licence for the new major version and install it. Updates within the major version of your licence are always covered.

## The user limit

With a licence that sets a limit, that limit applies; without one, Community's 8 active accounts. What counts as a user is explained in [Editions](../licensing.md): registered accounts that are active at the same time, not connected assistants, and locked accounts do not count.

If more accounts are active than the limit in force (after a removal, an expiry, or an update to a major version the licence does not cover), everybody keeps working. The licence report shows `over_limit` and a warning, and only **new accounts and reactivations** wait until there is room or a covering licence is installed again.

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

## Separating content from operations

In every edition an instance admin is admin in every team space: they run the system and can read all content. For organisations where the people who run Atrium should not read everything, for example because management and IT are different people, the Pro feature `content_separation` takes the automatic content access away. Personal spaces are closed to everybody in every edition.

*"Show the content access of the instance admins."* (`admin_content_access`, action `get`) tells the current setting and whether your licence includes the feature. *"Instance admins should no longer read all team spaces."* (`set` with `admins_read_content=false`, after confirmation) switches the access off; `true` brings the default back. The change applies at once and is recorded in the audit log (`admin_content_access_set`).

**What changes when it is off:**

- An instance admin reads, searches, reviews and changes only the spaces they are **members of**, with the role they hold there (or the default role of the space), like any other user. They no longer see the content of other team spaces, archived ones included, and an export (which needs the admin role of a space) covers only the spaces they are admin of as members.
- They still **manage every space**: create, change, archive and restore spaces, see and change members (`space_members`, `space_add_member`, `space_remove_member`), manage users, groups, logins, the allowlist, quotas and the audit log, and use all other `admin_*` tools.
- **Getting in when it is necessary:** an instance admin adds themselves to the space (*"Add me as reader to the HR space."*, `space_add_member`). That is possible by design and is recorded in the audit log with who, which space and which role, so that it cannot happen unnoticed. Check the audit log from time to time for `space_add_member` by instance admins.

**What the setting does not do:**

- It is a protective default with a trace, not a lock against an instance admin who wants to get in: every instance admin can add themselves to a space or switch the setting back, and the audit log records both. Keep the group of instance admins small, and use the audit log as the control.
- It does not change who may grant the instance admin right, publish skills of risk class C or delete skills; those remain with every instance admin.
- People with direct access to the database or backups are not affected by it (see [Operating the system](../user/05-instance-admin.md#operating-the-system)).

Without the feature in the licence, or when the licence lapses, instance admins read all team spaces again whatever was set; the tool tells so.

## Spaces below other spaces

In every edition spaces stand next to each other and every space has its own members. With the feature `space_hierarchy` a team space can sit below another one, for example company, department and team, and **memberships flow down**, so that people do not have to be added to every space again. Personal spaces never take part.

**The rule: the nearest entry wins.** For a person, Atrium looks at the space itself first and then upwards. The first space where the person has an entry, directly or through a group, decides their role. A person who is author in *Marketing* is author in *Marketing / Campaigns* as well, unless somebody gives them another role there:

| Entries of Anna | Role in Marketing | in Campaigns (below Marketing) | in Trade fair 2027 (below Campaigns) |
|---|---|---|---|
| author in Marketing only | author | author | author |
| author in Marketing, reader in Campaigns | author | reader | reader |
| reader in Marketing, curator in Campaigns | reader | curator | curator |
| nothing in Marketing and Campaigns, author in Trade fair 2027 | none | none | author |

So an entry in a lower space **overrides** what the space above would give, upwards as well as downwards, and somebody who is entered only in the lowest space neither sees nor reads the spaces above it. There are no entries that deny access. To close a space for the members of the space above, switch the inheritance off for it (below): then only its own entries count.

What else to know:

- **The default role of a space is not inherited.** If everybody logged in may read *Marketing*, that does not open *Campaigns*; the default role is set per space. It still counts on top of an inherited role.
- **An admin of a space is admin of the spaces below** that inherit, and so reads their content. Plan the top of a tree accordingly, or switch the inheritance off for a sensitive space.
- **Instance admins** keep what they have in every edition: admin in every team space, unless the content separation is on.
- **Archiving** a space hides it for everybody but instance admins and does not cut the access to the spaces below it: they keep inheriting from it. A space with sub-spaces cannot be deleted; move or delete them first.
- A tree has **at most five levels**, a space cannot sit below itself or below one of its own sub-spaces, and neither an archived space nor a personal space can be arranged.

### Arranging spaces

*"Put the space Campaigns below Marketing."* (`admin_space_parent`, action `set`, after confirmation) puts a space below another one. *"Make Campaigns a top-level space again."* (`parent` = `none`) takes it out. *"Campaigns should no longer inherit members from Marketing."* (`inherit_members=false`) closes the space and everything below it for the members of the space above; `true` opens it again.

- The tool is for **space admins and instance admins**. Putting a space below another one, and switching the inheritance on, need the admin right in **both** spaces (or the instance admin right), because the members of the space above gain access to the one below. Taking a space out and switching the inheritance off need only the space itself.
- Before you confirm, check who is affected: the members and groups of every space above gain, or with a change lose, access. The rights report ([instance admin guide](../user/05-instance-admin.md#rights-report)) shows it.
- Every change is recorded in the audit log (`admin_space_parent_set`) with the old and the new space above and the setting of the inheritance, and applies at once.

### Seeing the arrangement

- *"Show how my spaces are arranged."* (`admin_space_parent`, action `get`) shows for the spaces you administer their level, the space above, whether they inherit and the spaces below. A space above or below that you cannot see is left out.
- The **rights report** (instance admins) lists for a space below another one its parent, the inheritance setting and the **inherited** holders with the space each comes from. An admin who is only inherited counts: the report no longer says that the space has no admin, but points out where the admin comes from.
- `list_spaces` shows the role a person actually has in each space.
- `space_members` (space admins) lists the memberships of the space as `members` and, for a space below another one, what it takes over as `inherited_members`, with `inherit_members` telling whether the inheritance is on. Somebody with an entry of their own in the space is listed once, with that entry. The space a role comes from is named only if the caller can see it, otherwise the list says "a space above". `space_contacts` (everybody) adds the curators and admins taken over from above to the people to ask, without naming the space they come from.

### When the licence ends

An arrangement that exists keeps working, and nobody loses access on the day the licence ends: the spaces stay below each other and the rights stay as they are. Without the feature in the licence only changes that **narrow** rights are possible: taking a space out of the tree and switching the inheritance off. Putting a space below another one, moving it, and switching the inheritance on need the feature again. `admin_space_parent` tells whether your licence includes it, and a new licence lifts the restriction at once. Nothing is deleted.

## FAQ

**Do I need a licence to try the official image?** No. It runs as Community until you install one.

**The report says `wrong domain`. What now?** The licence is for another address than your `instance.base_url`. Tell behringer24 the address you run under and install the new licence.

**The report says `not covered` after an update.** See [Updating Atrium with a licence](#updating-atrium-with-a-licence).

**I removed the licence and have more users than Community allows. Are they locked out?** No. Every active account keeps working; only new accounts and reactivations are refused until there is room.

**Does Atrium phone home to check the licence?** No. The check is offline, and neither edition sends telemetry.

**Do I have to restart after installing a licence?** No.

**Can management read everything without running the system, or IT run it without reading everything?** Yes, with the feature `content_separation`: see [Separating content from operations](#separating-content-from-operations).

**Can I give somebody access to one sub-space only?** Yes. Enter them in that space and nowhere above it: they see it and not the spaces above.

**Can I keep one member of the space above out of a space below?** Not by name: there are no deny entries. Switch the inheritance off for that space and enter the people who belong there, or give the person a lower role there with an entry of their own.

**Does a flat instance change with this feature?** No. Without a space below another one everything works as before, and nothing in the answers of the tools changes.

**Who sees the licence?** Only instance admins, through `admin_license`. Other users see the edition and the release line in `server_info`.

**The tools `admin_license`, `admin_transfer_personal_space`, `reassign_owner` and `admin_space_parent` do not appear.** They exist only in the Pro edition. `admin_license` and `admin_transfer_personal_space` are for instance admins, `reassign_owner` and `admin_space_parent` for space admins and instance admins. Without the feature `offboarding` the two offboarding tools refuse with a message.
