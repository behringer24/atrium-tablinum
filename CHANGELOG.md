# Changelog

All notable changes to Atrium Tablinum. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow [Semantic Versioning](https://semver.org/). How a release is made is described in [docs/release.md](docs/release.md).

## [Unreleased]

### Added
- **Canary builds:** every push to the `dev` branch builds an image tagged `canary` (moving) and `canary-<short sha>` (immutable), never `latest` or a version tag. `server_info` reports the canary version. See [docs/release.md](docs/release.md).
- **Skill registry:** Atrium distributes skills, approved working instructions in the Agent Skills layout (`SKILL.md` plus text files), as a kind of its own, separate from knowledge entries. Versions are immutable once released and carry a SHA-256 digest; review, publishing and blocking are separate steps, a curator other than the author must approve, and class C skills can only be published by an instance admin. New tools: `list_skills`, `get_skill`, `get_skill_resource`, `store_skill`, `review_skill`, `publish_skill`, `block_skill`. Released skills are announced in the per-user instructions. Text only: scripts and binary files are refused and Atrium runs nothing. See [docs/skills.md](docs/skills.md).
- **Skills now have the same life cycle tools as knowledge entries:** `archive_skill` and `unarchive_skill` hide a whole skill from everyone who cannot restore it (`list_skills archived=true` shows the archive), `delete_skill` removes a skill with all versions for good (only instance admins in a team space, the owner in a personal space; the audit log keeps name and digests), and `unblock_skill` lifts a block (space admins and instance admins, reason required). An unblocked version is approved again, not released. See [docs/skills.md](docs/skills.md).
- New setting `skills.allow_self_approval` (default `false`) for installations with a single curator.

### Fixed
- **Personal space by slug:** the `space` argument now accepts the slug of the caller's own personal space, which `choose_space` offers but was rejected before, so an assistant could loop on the question. Skill tools also show the personal space as "My personal space" instead of the owner's name.

### Upgrade notes
- Migration `00007` creates the skill tables and the triggers that freeze released versions. Migration `00008` adds the archive columns and lets a blocked version go back to approved (and only there).

### Changed
- Everything is English now: documentation, code comments, tool descriptions, the MCP instructions, error and confirmation messages, log texts and the browser pages of the login flow. Assistants still answer users in the user's language.
- The displayed name of a personal space is "My personal space". The space matcher keeps accepting the German aliases ("mein persönlicher Bereich" and others) and also accepts English ones ("my personal space", "my space", "private").
- Log attribute keys are English (`duration`, `count`, `stored`, `configured`). Adjust log searches and alerts that used the German keys.
- The reason of `login_denied` audit entries is English. Existing entries and stored data keep their German text.

Planned work is listed in the [ROADMAP.md](ROADMAP.md).

## [0.3.0] - 2026-11-03

### Added
- **Archive with restore:** archived entries disappear from the active knowledge base but are kept. Whoever may archive (authors their own entries, curators and admins of the space) can list them with `list_knowledge status=archived`, search only the archive with `search_knowledge archive=true`, and bring them back with `unarchive_knowledge`. A restored author entry in a space that requires approval goes back to review. See [docs/archive-and-context.md](docs/archive-and-context.md).
- **Instance context:** `admin_context` sets a text that tells every connected assistant what this Atrium instance contains. It is shown together with the descriptions of the spaces the respective user may read.
- **Questions users can ask:** `space_contacts` names the curators and admins of the user's spaces, `server_info` the running version (set by the release build).
- **Managing logins:** `my_sessions` and `revoke_my_sessions` for users, `admin_revoke_sessions` for instance admins.
- **Login allowlist at runtime:** `admin_allowlist` manages allowed e-mail domains, single addresses and Entra tenants without a restart. Entries only widen what `config.yaml` allows. Rejected logins appear in the audit log as `login_denied` (with provider and reason, without the address).

### Changed
- Content read from the store is marked as untrusted data, in the results and in the MCP instructions.
- `admin_context` (set, clear) and changing a space description need `confirm=true`. The previous text is kept in full in the audit log.
- Reads are rate limited (`rate_limit_reads_per_minute`, default 120).
- A session ends after `auth.session_max_age` (default 90 days), even if it is used continuously.
- An admin cannot transfer their own personal space to themselves.

### Security
- Accounts are no longer linked by e-mail address automatically. This is only done with `auth.link_accounts_by_email: true` and is then written to the audit log as `account_linked`.
- `dev_mode` only runs with a `base_url` on localhost and a mandatory `dev_token` of at least 16 characters. The token is neither generated nor logged.
- The message shown for a rejected login no longer contains the e-mail address.

### Upgrade notes
- Migrations `00005` (session start) and `00006` (login allowlist) run automatically on first start.
- If you run several login providers that share e-mail addresses, you must enable linking deliberately.
- `dev_mode` needs the new mandatory settings, otherwise the server does not start.

## [0.2.0] - 2026-11-02

### Added
- **Drafts on request:** `store_knowledge draft=true` stores an entry as a draft in any team space, even without required approval and also for admins.
- **Curator review:** `review_knowledge` releases a draft or sends it back with a mandatory note (`review_note`). A revised rejected entry goes back to draft. Curators cannot release their own draft, admins can (marked `self` in the audit log). `list_knowledge` filters by status. See [docs/approval.md](docs/approval.md).

## [0.1.1] - 2026-11-02

### Fixed
- The MCP endpoint serves clients without a session id by running stateless.
- `.env.example` explains how to choose `POSTGRES_PASSWORD`.

## [0.1.0] - 2026-11-02

First release: MCP server in Go with spaces and roles, hybrid search (pgvector and full text), OAuth 2.1 with Google, Entra and generic OIDC, approval workflow, audit log, offboarding and a selectable embedding backend (TEI, OpenAI compatible, Bedrock). The image is published to the GitHub Container Registry.

[Unreleased]: https://github.com/behringer24/atrium-tablinum/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/behringer24/atrium-tablinum/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/behringer24/atrium-tablinum/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/behringer24/atrium-tablinum/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/behringer24/atrium-tablinum/releases/tag/v0.1.0
