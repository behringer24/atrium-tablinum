# Commercial editions and licence files

**Status:** the model is decided. The public tree has the extension points (`internal/edition` and three hooks in `internal/mcp`) and a fixed Community limit, and **contains no licence code at all**: verification, the issuer public keys, `admin_license` and the issuing tool live in the private Pro repository (`atrium-tablinum-pro`) and are laid over this tree at build time. Everything marked as a later phase is not built. Where a decision belongs to the owner and not to engineering it is marked **Open** and carries a recommendation. Nothing here is a legal text; the terms a customer signs, and the reading of the EU Cyber Resilience Act below, need a lawyer.

## Goal

Atrium Tablinum is sold to organisations of different sizes and is usable for free where that is fair:

- a **Community** edition: public source, free for small use, with a fixed limit and no licence support;
- a **Pro** edition: the same product with extra code that is not public and with limits lifted;
- a **licence file** that a customer puts on their server, which only the Pro build reads: it switches limits and features on, without a restart and without the server calling anybody.

Non-goals: an uncircumventable copy protection (impossible for a Go binary, see [What cannot be protected](#what-cannot-be-protected)), a licence server the customer's instance must reach, and hosting Atrium for third parties (the Elastic License 2.0 already forbids that for everybody else).

## Where we start

- Source is public (the repository becomes public as it is: its history never contained Pro code), the licence is the [Elastic License 2.0](../../LICENSE): use, modify and redistribute allowed; **offering it as a managed service to third parties is not**, and neither is **circumventing or removing licence key functionality**. The second clause is what makes a licence-file model enforceable by contract and the main reason to stay with ELv2 instead of a copyleft licence. It bites on the Pro build, which has licence key functionality; for the Community limit it is a question for the lawyer (below).
- ELv2 is **source-available, not open source** in the OSI sense. Say so in every public text.
- The public tree is one monolithic tree that builds and tests alone. The Pro files are laid over a copy of it by the private pipeline.

## Options considered

| Option | How | Verdict |
|---|---|---|
| **A. Licence file in the open code** | Every feature is in the public repo; limits and some features check a signed licence | Simple, one build. The protection is **legal only**. Fine for limits |
| **B. Build tag plus private overlay** | The public repo defines small extension points with free defaults; a private repository holds Pro code as files with `//go:build pro`; the private pipeline lays the overlay into the tree of the public tag and builds with `-tags pro` | **Chosen, and built.** The code is not public, the overlay can use `internal/` packages, and the licence code itself is part of the overlay |
| C. Second Go module with an exported API | The private module imports the public one | Clean boundary, but `internal/` is invisible to other modules, so a stable public `pkg/` API would have to be designed and kept. More promises |
| D. Go `plugin` package | Load Pro code at run time | **Rejected.** No Windows, identical toolchain and dependency versions required, fragile |
| E. Separate service (sidecar) | Pro features as their own process | Only for heavy add-on services |
| Phone-home licence server | The instance asks a service | **Rejected.** Single point of failure, privacy cost, many company servers have no outbound internet |
| Obfuscation, hardware binding | Make patching hard, tie to a machine | **Rejected.** Cost without lasting effect; hardware binding breaks restores |

## Decision

**B, with the licence code inside the overlay: two builds from one code base, one public image.**

- **Community build:** the public repository alone (`go build ./cmd/server`). It has **no licence verification, no issuer keys and no `admin_license`**, and a fixed limit of **8 active accounts** (`edition.CommunityMaxUsers`, a constant: there is no configuration key that lifts it). A `license:` section in `config.yaml` is refused at start with a message that says so. Anybody can build it and verify that it contains no licence or telemetry code.
- **Pro build:** the same tree with the overlay of the private repository laid over it and built with `-tags pro`. The overlay brings the licence manager, the issuer public keys, `admin_license`, the issuing tool and every Pro feature. Pro features need a covering licence; the licence also lifts the user limit.
- **One public image.** The official image is built by the private pipeline from the public tag plus the overlay (`GO_TAGS=pro`) and published as a public package. Without a licence it behaves as Community; a licence unlocks Pro, with no change of image and no registry credentials. Whoever wants a pure Community binary builds from the public repository.

Why the licence code moved out of the public tree: the protection of Pro rests on the Pro features **not being public**, not on the check (a Go binary can always be patched). With the check public, the Community build carried code and keys it can never use, and a configuration key (`community_max_users: 0`) lifted the one hard limit by itself.

Protection, honestly: the limit by the contract and the friction of patching, Pro features additionally by the code not being public. The documents say so.

## Editions and what is gated

**Principle: security is never sold.** Roles, four-eyes review, risk classes, blocking, the audit log, login checks, rate limits, quotas, **and security updates** stay in Community. A customer must not have to pay to be safe. What is licensed is scale, integration and governance extras, and the right to new versions (below).

| Area | Community | Pro (candidates) |
|---|---|---|
| Knowledge, knowledge types, responsible people and review dates as data, skills, search, spaces, roles, review, audit log, handoffs, personal spaces | yes | yes |
| Users | up to **8 active accounts** (decided, fixed) | higher or unlimited by licence |
| Spaces, quotas | the built-in defaults, runtime quotas for the instance admin | higher ceilings by licence |
| Login providers | as today | group synchronisation, SCIM provisioning |
| Offboarding | **locking an account and ending its sessions** (`admin_revoke_sessions`), the retention period and its purge | **the guided workflow**: handing a personal space on (`admin_transfer_personal_space`), reassigning responsibilities (`reassign_owner`), the hints that lead through it |
| Review dates | the dates, the filter for entries that are due | **enforcement**: due-date reports, reminders and notifications by mail, Teams or Slack |
| Audit | the log in the database | export to a SIEM, hash chain, retention rules |
| Importers | the ingest base | connectors to Confluence, SharePoint, Notion |
| Support | community | support and SLA (a service, not a feature) |

The list is a menu, not a promise. Multi-instance administration is **dropped**: one instance serves exactly one organisation. **Decided:** the free limit (8 active accounts). **Recommendation:** group sync and SCIM first in Pro, then the guided offboarding and the review reminders, support sold alongside as a subscription.

Features are only ever moved to Pro **before the public repository exists**. Taking a released free feature away would break trust, so what is free now stays free.

**Built so far:** neither the guided offboarding nor the review enforcement has been moved: the guided offboarding is still in the public tree (`admin_offboard_user` with its hints, `admin_transfer_personal_space`, `reassign_owner`), the review reminders do not exist yet. Moving offboarding means a free basic `admin_offboard_user` (lock, end sessions, delete handoffs) and the rest in the overlay; it is the next step of the split, on its own branch.

**Further editions need no code.** An edition is the name of an offer, not a switch: the code only ever asks for a feature (`license.Has("scim")`) or a limit (`users`). An **Education** or non-profit offer is a licence issued with `-edition education` and its own features and limits; who qualifies is settled by the contract, not by the software. Keep edition names lower case and one word, so that the register and the reports stay consistent.

## The maintenance model

A customer buys **the right to use what they have, forever, and to receive new versions for a period**. This is the usual shape of software that is bought and kept, and it needs no renewal for running software, no revocation and no call home.

- A licence carries **`maintenance_until`**, a date. Every build carries the **date of its release line** (below). A build is covered when its **line date is not after `maintenance_until`**. Minor and major lines are treated alike: it is only a date.
- A covered build runs with everything the licence grants, **for ever**. Nothing expires while the customer stays on a covered version.
- **Patch releases** of a covered line are covered by construction: they carry the line's date, not their own. They can be installed whenever the customer likes, however old the setup is, **as long as the line is still maintained** (see [Release policy](#release-policy)).
- A **newer line** is covered only if released before `maintenance_until`. A customer whose maintenance ended keeps the last covered line for ever and takes its patches; for the next line they buy maintenance again.
- **Lifetime** is the same mechanism with a distant date (for example 2099). One year, three years and lifetime differ only in the date in the file.
- **Subscription** (features off when payment stops) stays possible for customers who want it, with the optional `expires` field; it is also what trial licences use. `expires` is the only field that needs the clock.
- **Kulanz** has two clean forms, neither of which falsifies a version number: issue the customer a **new licence file with a later `maintenance_until`** (free), or ship an **"amnesty" release** whose line file carries an earlier **epoch** (below), so that every licence valid from that date on covers it. A version tag is never relabelled (`v3.4.3` is never `6.2.1`: it would break the promise a patch number makes, drag customers through irreversible database migrations when tools update patches automatically, and make `server_info` and the audit log lie).
- **Renewal** extends from the previous end date when it is renewed in time, so that no gap arises. Renewal is the installation of a new file, once a year at most, by an instance admin (`admin_license`), without a restart. A customer who does not want new versions never touches the file again.
- Withdrawing a paid version is **not possible** (that would need a call home). Disputes are a matter for the contract; the technical lever is the state `not covered`, which switches the Pro features off for a build beyond the maintenance.

Example, with a customer covered until 1 May 2023:

| Build | Line date | Covered |
|---|---|---|
| 3.4.0 (line of May 2022) | 2022-05-10 | yes |
| 3.4.9, built in 2026 | 2022-05-10 | yes (patch of a covered line) |
| 3.5.0 (January 2023) | 2023-01-15 | yes |
| 3.6.0 (June 2023) | 2023-06-02 | no |
| 3.6.1 | 2023-06-02 | no |

### How a binary knows its line

- A small file in the repository, `internal/product/line.txt`, names the **line** and its **date**, and optionally an **epoch**:

  ```
  line 3.4
  date 2022-05-10
  epoch 2022-01-01      # optional: amnesty, see above
  ```

- It is embedded with `go:embed`, so the date travels with the source. A build of `3.4.2` years later, made from the branch `release/3.4`, still knows the date of `3.4.0`. There is **no table to maintain**.
- The coverage date of a build is the **epoch if present, else the date**.
- The **build date** is separate (a linker flag, the commit date for reproducible builds), shown by `server_info` and `admin_health` for support, and **never used for licensing**. For `3.4.0` the two dates are equal, from `3.4.1` on they differ.
- Nothing compares with the system clock: the check compares two fixed values, so there is no expiry at midnight and no clock to set back.
- **Guard in CI** (the one place a slip would cost money: a forgotten update would make `3.5.0` carry the date of `3.4`): for `vX.Y.0` the file must name `X.Y` and today; for `vX.Y.Z` with Z above 0 it must be byte-identical to the file in the tag `vX.Y.0`. A helper script starts a line in one step (writes the file, commits, tags). A backport never takes the file along.

## The licence file

A small signed statement, verified **offline**, much like a JWS: the issuer signs the exact bytes of a JSON payload, and the token is `atrium-license-v1.<payload>.<signature>` (both base64url).

```
{ v, id, licensee, domain, edition, features[], limits{...}, issued, maintenance_until, expires?, issuer_key }
```

| Field | Meaning |
|---|---|
| `v` | format version |
| `id` | licence id, for the register and for support |
| `licensee` | display name of the customer |
| `domain` | the instance, matched against the host of `instance.base_url` (a stolen file is useless under another domain; a restore onto new hardware keeps working) |
| `edition` | the name of the offer, e.g. `pro` or `education`: an open string, shown by `server_info` and `admin_license`, that switches nothing by itself; what a licence grants are its `features` and `limits` |
| `features` | named switches, e.g. `group_sync`, `scim`, `audit_export` |
| `limits` | named ceilings, e.g. `users`; a missing key means "the Community default" |
| `issued`, `maintenance_until` | dates; `maintenance_until` as above |
| `expires` | optional, for trials and subscriptions |
| `issuer_key` | the id of the issuer key that signed it |

- **Trust anchors:** **five** Ed25519 public keys are compiled into the Pro build (`internal/license/anchors.txt` in the **Pro repository**, embedded), one id per line. Only one signs; the others are cold spares, so rotating the signing key needs no release. A licence names which key signed it. The public keys are public information; they sit in the private repository so that the Community build carries no licence code and no keys, not because they are secret.
- **The issuing tool is in the Pro repository** (`cmd/atrium-license`): `genesis` creates the key pairs, `issue` signs a licence, `verify` and `show` check and print one. The secret is the private keys, never the procedure, and the format is not meant to be a secret either. **What must never be in any repository:** the private keys, the register of issued licences (customer and commercial data), and the real licences. `.gitignore` catches the usual file names; it is a net, not the plan. The practical side (key ceremony, issuing, testing) is in the Pro repository's `docs/licensing.md`.
- **Private keys** are generated **offline**, on a machine that is not on a network, stored on removable media in more than one physical place, and never touch a server, a CI system or this repository. The public halves go into `anchors.txt`.
- **Installing:** `license.file` in `config.yaml`, or at run time by an instance admin with `admin_license` (`get`, `install`, `remove`, with `confirm`), stored in the database, effective at once, audited (`license_install`, `license_remove`), shown by `admin_health` and `server_info`. `admin_license` also compares the active accounts with the user limit in force and warns when it is reached, so that an instance over the limit is visible without anybody being locked out.
- **States** the instance reports: `none` (Community), `valid`, `not covered` (the build is newer than the maintenance: Pro features off, the message names the dates), `expired` (the optional `expires` passed), `wrong domain`, `invalid` (signature or unknown key). In every state except `valid` the limits are the Community defaults, **and content stays readable and exportable**. Nothing existing is blocked or deleted, and nobody is logged out because of a licence. A valid licence carries a notice from 60 days before its `expires` date and before the end of maintenance, so that a renewal is never a surprise.
- **No call home.** Verification needs the embedded public keys and, for `expires`, the clock.

## How limits are enforced

Limits already exist as quotas with runtime overrides. In the Pro build the licence adds a **ceiling**: an instance admin may override within the licensed ceiling, and without a licence the ceiling is the Community default (not built yet). The **user limit** is the one hard limit: it applies when a **new account** would be created and is a refusal with an explanation, never an effect on existing accounts. In the public build it is the constant `edition.CommunityMaxUsers` (8) behind `edition.Running.MaxUsers()`; in the Pro build the licence manager answers instead and can lift it. There is **no configuration key** for it in either build: a limit the customer can set to 0 is no limit.

## Extension points and the overlay (option B, built)

The public tree has what the first Pro feature needs, with a free default for each (so it builds and is tested alone):

- **`internal/edition`:** `Register` (called from `init()` of the overlay), `Start` (called by the server once the database is ready), the interface `Running` (`Name`, `MaxUsers`) and the optional `Prober` for `admin_health`. It hands the edition the pool, the base URL, a logger and the opaque `license:` section of `config.yaml` (`config.Config.License` is a raw YAML node; the Community build refuses a non-empty one).
- **`internal/mcp/edition.go`:** `editionTools` (extra tools), `editionDescriptions` (their descriptions, rendered like the built-in ones) and `editionSupport` (named parts of `admin_support_bundle`, nested under `edition`).

Everything else the overlay needs it reaches directly, because the overlay files live **inside the module** (also inside `internal/mcp`, so unexported helpers such as `addTool` are usable) and are rebuilt against every public release. Keep the hooks few: every hook is a promise to the private side, and each new one should be justified by two uses.

The private repository holds the files under `overlay/` at their final paths, a build script (`scripts/build.sh`: lays the overlay over a copy of a public tag or of the working tree, builds and tests with and without the tag) and its pipeline. Files that hook in carry `//go:build pro`, so the combined tree built without the tag is still the Community build.

## Release policy

A licence model is only as workable as the patch work behind it. The facts that drive it:

- **Every maintained line needs its own security patch.** A vulnerability found today is fixed once and then **backported to each maintained line** (`release/6.2`, `release/5.4`, …), each tested and released (`6.2.3`, `5.4.7`, …). The fix is written once; every extra line costs the cherry-pick, the conflicts, the full test run, the build and the release.
- **The number of lines is lines per year times years of support.** At five years of security fixes: 12 feature lines a year mean 60 branches, 4 mean 20, **2 mean 10**, 1 means 5. Releasing every feature as it is finished is not sustainable.
- **Rules that keep it manageable:**
  - **Separate development from delivery.** Develop as fast as you like (`dev`, the `canary` image for testers); deliver to customers on a **fixed rhythm**, two feature lines a year (it also suits enterprise customers: updates become plannable). Between the lines only patches.
  - **A major only when something breaks** (configuration, data model), not as marketing.
  - **A patch contains fixes only**: no features (they would reach every customer of the line for free) and **no schema migration** (migrations only go forward and are numbered; a fix that needs one in an old line collides with the migrations later lines already have).
  - **Automate the backport:** CI applies a fix to every maintained branch, runs the full suite per branch against a real database, builds the images; dependencies (the Go toolchain, libraries) are updated per branch (Dependabot per branch).
- **Long-term lines** (only some lines are maintained for the full term, the others until the next line plus a transition) would be cheaper, but see the next section before promising it.

## The EU Cyber Resilience Act

The EU Cyber Resilience Act ([Regulation (EU) 2024/2847](https://eur-lex.europa.eu/eli/reg/2024/2847/oj)) is product law and applies **regardless of what a licence says**. This is a reading, to be confirmed by a lawyer and the text of the regulation.

- **Support period of at least five years** for security updates, counted from placing on the market, unless the product is expected to be used for less (Article 13). Security updates must be available without delay, and remain available for at least ten years or the rest of the support period, whichever is longer (Article 13, per the commentary, paragraph 9).
- **Reporting** of actively exploited vulnerabilities and severe incidents within **24 and 72 hours**: applies since **11 September 2026**, per commentary also to products already on the market. **Full application from 11 December 2027** (conformity, technical documentation, CE marking, a software bill of materials, security by design).
- **Scope:** commercially distributed software for organisations makes the vendor a manufacturer. The exemption for free and open-source software covers only what is supplied **outside a commercial activity**, and what counts is the commercial activity, not the licence. **Open core is likely in scope, including the free core**, if it belongs to the same commercial offering. ELv2 is not open source, so the exemption most likely does not apply; that conclusion is ours and not confirmed.
- **What it means here:**
  1. The patch duty is **independent of the maintenance**. Security fixes are owed for the support period to every customer, paid or not, which fits "security is never sold". Whether they must be **free of charge** is, to our knowledge, in Annex I; confirm.
  2. A security policy of "current and previous line for 12 to 24 months" is **too short**. Plan **five years of security fixes per released line** unless the lawyer confirms another reading.
  3. Article 13(10) lets a manufacturer, under strict conditions, ensure compliance only for the **newest** version. To our recollection one condition is that users of older versions get the newest **free of charge**, which would **collide with paid feature lines**; and the other obligations for all versions remain. A "long-term lines only" policy depends on this and **must not be promised before it is checked**.
  4. Offering old customers a **free upgrade** (the amnesty release above) helps with 13(10); the version label stays honest.
  5. **Needed now:** `SECURITY.md` with a reporting route and deadlines, a process for vulnerabilities and incidents (who reports to whom within 24 hours), a **software bill of materials** from the build (the pinned dependencies and `govulncheck` are the base), and a **declared support period per version** in the documentation.
- Consumer-law duties to update digital products apply to sales to consumers and not to business customers.

## Build, release and distribution

- **The public image** is built by the **private** pipeline: it checks out the public tag, lays the overlay over it, runs the full suite in both modes (`scripts/build.sh test`), builds with this repository's Dockerfile and `--build-arg GO_TAGS=pro`, signs the image (cosign) and attaches an SBOM and provenance, and publishes it as a **public package**. No registry credentials for customers; air-gapped sites mirror the public image.
- **This repository's workflow** tests the public tree and builds the pure Community image for those who want it (`community` tag). It is the only image whose content can be rebuilt from public source.
- **The private registry as a lever is given up.** The case it served (a customer pulling a `latest` beyond their maintenance by accident) is covered by the state `not covered`: Pro features are off, the limits are the Community ones, all data stays accessible.
- **Versions** are identical in both builds; `server_info` and `admin_health` report the edition, the line and (in Pro) the licence state.
- **Documentation** states per feature which edition has it. The user guides and design documents are public; only the practical licence operations (key ceremony, issuing, register) live in the Pro repository.
- **The Pro licence** is a separate proprietary licence for the overlay; the image carries both texts (ELv2 for the public part, the Pro terms for the overlay). Settled with the lawyer.

## What cannot be protected

A Go binary can be patched and source in a customer's hands can be edited. The licence therefore rests on three things, in this order: the contract (ELv2's clause against circumventing licence key functionality, plus the customer agreement), the fact that Pro code is not public, and the registry, support and updates that only licensed customers receive. Do not spend effort on hiding the check; spend it on making licensing pleasant for honest customers (clear messages, no outages, no surprise lock-outs).

## Legal and commercial prerequisites

Not engineering tasks; the design depends on them.

- **Review of ELv2 and the customer terms** by a lawyer, including the behaviour on `not covered` and `expired`, and the CRA reading above.
- **Contributor agreement (CLA).** If outside contributions are accepted and Pro is sold, contributors must agree that their code may be part of a commercial edition. Decide before the first external pull request is merged.
- **Name and trademark:** the checks done for the name hold for a commercial product; keep the note current.
- **Public wording:** "source-available", never "open source".
- **Dependencies:** the licence audit covers the current set (all permissive); repeat it when Pro adds dependencies.
- **Data protection:** a licence names a customer and a domain; the register is personal and commercial data and is handled as such.

## Risks

| Risk | Mitigation |
|---|---|
| Too many lines to patch | Two feature lines a year, automation, patches without features or migrations |
| The CRA reading is wrong | Lawyer before any promise about support periods or long-term lines |
| A forgotten `line.txt` gives a new line away | The CI guard against the tag `vX.Y.0`, the start-line script |
| The extension points grow into an unstable API | Few hooks, each justified by two uses; Community tests exercise the free defaults |
| Customers dislike a hard limit | Only new accounts are refused, never existing ones; clear message |
| A signing key is lost or leaked | Five trust anchors from day one, spares offline in more than one place; a leaked key is replaced by promoting a spare, bounded by the licence lifetimes |
| A customer upgrades by accident beyond the maintenance (the `latest` tag) | The build says `not covered` clearly, switches the Pro features off and keeps all data accessible (there is no registry token to limit the pull) |
| A paid version cannot be withdrawn | Accepted; the contract and the registry are the levers |
| Community feels crippled | Security and governance basics stay free; the limit is generous enough for a real small team |

## Phases

0. **Decisions** (below), legal review started, CLA decided.
1. **Licence core** (**built**): the format and verification with five embedded trust anchors, `line.txt`, the line and build date, `license.file`, `admin_license`, `admin_health` and `server_info`, audit events, the issuing tool in `cmd/atrium-license`, tests, documentation.
2. **Limits** (**partly built**: the user limit with a configurable Community default): licensed ceilings for the existing quotas; the final Community defaults.
3. **Release mechanics:** the CI guard for `line.txt`, the start-line script, release branches per line, automated backports, the SBOM.
4. **Extension points and the overlay** (**built**): `internal/edition`, the three hooks in `internal/mcp`, the Pro repository with the licence code as its first overlay, `scripts/build.sh`, the `GO_TAGS` build argument of the Dockerfile. Not built: the pipeline of the Pro repository.
5. **Moving the guided offboarding and building the review reminders** into the overlay (see "Editions and what is gated"), then the first new Pro features (group sync, SCIM, audit export) behind the gate (`Edition.Has`-style checks live in the overlay).
6. **Distribution:** the private pipeline that publishes the public image, signing, SBOM, commercial terms, price list.

## Open decisions

| Decision | Recommendation |
|---|---|
| The free limit | **Decided: 8 active user accounts**, the only hard Community limit. It counts registered accounts that are active at the same time, not connected assistants; locked accounts do not count; reactivating needs room |
| First Pro features | Group synchronisation and SCIM, then audit export |
| Price model | Per instance with a user band, support included; decide after the first customer talks |
| Maintenance term | One year as the standard, three years and lifetime as options; renewal extends from the old end date |
| Feature lines per year | Two |
| Support period per line | Five years of security fixes until the lawyer says otherwise |
| Hosted offering by the owner | Not now; the model does not depend on it |
| Make the image public | **Decided: yes**, one public image built from the overlay; the licence unlocks Pro. A package only becomes public once, so do it together with the lawyer's review of the licence texts |
| Licence code in the public tree | **Decided: no.** It lives in the Pro repository; the Community limit is a constant |
| Name of the public repository | `atrium-tablinum` (the Go module path already is `github.com/behringer24/atrium-tablinum`); the private one is `atrium-tablinum-pro` |
| ELv2 clause on the Community limit | Ask the lawyer: with no licence key functionality in the public build, does editing the constant circumvent it? The limit stays protected by the contract either way |

## Rejected, for the record

Go plugins, phone-home licence checks, hardware binding, binary obfuscation, gating security features or security updates, hard lock-outs, a licence that expires for running software, relabelling a newer build with an older version number, and a copyleft licence for the public code (it would remove the clause that protects licence keys).
