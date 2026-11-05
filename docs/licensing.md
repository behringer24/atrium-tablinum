# Licences: using, issuing and testing

How an instance runs with a licence, and how the owner issues one. The reasons and the model are in the [design](design/licensing.md); this is the practical side.

## In short

- Without a licence an instance is **Community**: everything works, with the Community limit: **8 active user accounts** (`license.community_max_users`, default 8, 0 = unlimited).
- A **licence file** is a signed line of text. An instance admin installs it with `admin_license`, no restart, or it is named in `license.file`. It is checked **offline** against public keys compiled into the binary and never calls anybody.
- A licence is bound to the **host of `instance.base_url`**, lists **features** and **limits**, and carries **`maintenance_until`**: a build is covered when the release line it was built from is dated on or before that day, **for ever**. New versions released later need a licence with a later date. Patch releases of a covered line are always covered.
- No licence state ever blocks content: in every state other than `valid` the instance runs as Community, and what is stored stays readable and exportable. A user limit only refuses **new accounts**, never existing ones, and never the administrators in `bootstrap_admins`.

**What counts as a user:** the registered accounts that are **active at the same time**. It is not the number of assistants connected (one user with Claude, ChatGPT and Claude Code is one user). **Locked accounts** (after `admin_offboard_user`) do not count, and accounts are never deleted, so offboarding frees the seat. Reactivating a locked account needs room again, so the limit cannot be passed by offboarding and reactivating. The administrators in `bootstrap_admins` count as active accounts but are never refused, so the limit cannot lock them out.

## For the instance admin

*"Show the Atrium licence."* (`admin_license action=get`) reports:

| | |
|---|---|
| `status` | `none` (Community), `valid`, `not covered` (this build is newer than the maintenance: the text names the dates), `expired` (the optional `expires` date passed), `wrong domain`, `invalid` (unreadable, unknown signing key, bad signature) |
| `edition`, `licensee`, `features`, `limits`, `maintenance_until`, `expires` | what the licence says; a limit of 0 is unlimited |
| `source` | `installed` (by an admin, stored in the database) or `file` (`license.file`) |
| `build` | the version, the **release line** and its date (the date compared with `maintenance_until`), and the commit date for support |
| `users` | `active` accounts now, the user `limit` in force (the licensed one, else the Community limit; 0 = unlimited) and `over_limit` |
| `warning` | only when the active accounts have reached the limit: existing accounts keep working, new accounts and reactivations are refused until there is room |

*"Install this licence: atrium-license-v1.…"* (`action=install`, after confirmation) refuses a licence that is unreadable, signed with an unknown key or bound to another domain, and accepts one that does not cover this build, so that the report can say why it has no effect. `action=remove` deletes the installed licence (one from `license.file` stays); the question before the removal names how many accounts are active and what the Community limit means for them. Both are audited (`license_install`, `license_remove`, with licence id, licensee, edition and maintenance date, never the licence text).

**More accounts active than the limit allows?** That happens when a licence is removed, expires or does not cover an updated build while more accounts are active than Community allows. Nobody is locked out: every active account keeps working, and the report shows `over_limit` with a warning. Only new accounts and reactivations are refused until fewer accounts are active than the limit, or a covering licence is installed again.

`server_info` shows every user only the edition and the release line. `admin_health` has an informational `license` check that never makes the instance unhealthy.

**Renewal** is installing a new file once the maintenance is extended: at most once a year, in the running instance. Someone who does not want new versions never touches the file again.

**After an update the licence says `not covered`?** The build is of a line newer than the maintenance. Pro features are off and the limits are the Community ones, all data stays accessible. Go back to the covered version, or renew the maintenance.

## For the owner who issues licences

Everything here runs on **your** machine. The tool is `cmd/atrium-license`.

### One-off: the issuer keys

Five Ed25519 key pairs, created **offline**: on a machine that is not on a network, into a directory on removable media that is not inside any repository (the tool refuses a directory inside a git working tree and a directory that already holds keys).

```bash
go run ./cmd/atrium-license genesis -out /media/offline/atrium-issuer -count 5
```

It writes one private key file per pair (`k<8 hex>.key`, mode 0600) and an `anchors.txt` with the public halves. Then:

1. **Paste the lines of `anchors.txt` into `internal/license/anchors.txt`** and rebuild. Public keys are not secret; they belong in the binary and in the repository.
2. **Move the `*.key` files to offline media**, keep copies in more than one physical place, and delete them from the machine that made them. They are the one thing in this system that matters; whoever holds one can issue licences.
3. Use **one** key to sign. The other four stay unused: if the signing key is lost or leaked, you sign with a spare, and no release is needed because every build already trusts all five.

Until real keys are in `anchors.txt`, no licence verifies and every instance is Community.

### Issuing

```bash
go run ./cmd/atrium-license issue -key /media/offline/atrium-issuer/k1a2b3c4d.key \
    -licensee "Example GmbH" -domain atrium.example.com \
    -months 12 -features scim,group_sync -limit users=200 \
    -register /secure/place/issued.jsonl
```

It prints the licence (one line, `atrium-license-v1.…`) to stdout and a summary to stderr, and appends the licence and its claims to the register if `-register` is given.

| Option | Meaning |
|---|---|
| `-domain` | the host of the customer's `instance.base_url` (a licence for another host is refused at installation) |
| `-months N`, `-until DATE`, `-lifetime` | `maintenance_until`: N months from today, a date, or 2099-12-31 |
| `-features`, `-limit name=number` | what it switches on and the ceilings (0 = unlimited); a limit that is not set falls back to the Community default |
| `-expires DATE` | optional: no effect after this day (trials, subscriptions); leave it out for a normal licence |
| `-edition` | the name of the offer, `pro` by default, any other such as `education` (lower case, one word); shown by `server_info` and `admin_license`, it switches nothing by itself: what a licence grants are `-features` and `-limit` |

For a renewal issue a new licence and extend `maintenance_until` from the **previous end date** when the customer renews in time, so that no gap arises. For a kulanz, issue a new file with a later date, free of charge. A whole line can be covered more widely with an **epoch** in `line.txt` (see below).

Check a licence: `go run ./cmd/atrium-license verify -base-url https://atrium.example.com <licence>` (signature, content, and how it evaluates for that instance and this source tree's line).

### The register and what never goes into the repository

The **register** (who got what, when) and the **licences** are customer and commercial data: keep them outside the repository, with the keys. `.gitignore` catches `*.key`, `register/` and the usual licence file names; that is a net for slips, not the plan. The tool itself, the format and the public keys are public on purpose: the secret is the private keys, never the procedure.

### The release line

Every build knows the release line it belongs to from `internal/product/line.txt`, embedded in the binary:

```
line 3.4
date 2022-05-10
epoch 2022-01-01      # optional
```

- **`date`** is the day the line (`X.Y.0`) was released. All patch builds of the line carry it; they are covered like `X.Y.0`.
- **`epoch`** (optional) replaces the date for coverage: an "amnesty" release sets it to an earlier date, so that every licence valid from then on covers it, without relabelling any version.
- When a **new line starts**, set `line` and `date` (and no epoch) in the commit that gets the tag `vX.Y.0`. The branch of the line keeps the file for all its patches; a backport never takes it along. Until the release mechanics are automated (design, phase 3) this is a manual step, and a forgotten update would give the new line away: check it before tagging.
- The **build date** (the commit date, passed as a build argument) is for support only and never used for licensing.

### Testing it end to end

1. `genesis` into a temporary directory outside the repository, paste the anchors into `internal/license/anchors.txt` (do not commit test keys).
2. `go run ./cmd/server -config config.yaml`, log in as an instance admin.
3. `issue` a licence for the host of your `base_url`, `-months 12 -limit users=5`.
4. In the assistant: *"Install this licence: …"*, then *"Show the Atrium licence."*
5. Try the limits (`license.community_max_users: 2` without a licence refuses the third new account), `-until` a past date (`not covered`), another `-domain` (refused), and `-expires` yesterday (`expired`).

Remove the test keys from `anchors.txt` again before committing.
