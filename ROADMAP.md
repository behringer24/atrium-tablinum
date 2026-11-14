# Roadmap

What is planned next and what is deliberately left open. The order within a section is not a commitment; commitments only exist as entries in the [CHANGELOG](CHANGELOG.md).

Ground rules: Atrium is offered commercially to companies of different sizes. Security comes before convenience, risky features are off by default, and one instance serves exactly one organisation. The AI assistant is the main interface: not every feature needs a web UI, but every important feature needs a clear, safe and consistent tool interface. Atrium is the durable, vendor-neutral memory of the organisation, not another wiki.

The guiding question for all knowledge features: does an assistant find the right, current entries the user may see, classify them correctly and cite them traceably, whichever AI system the user works with?

## Next

### Reliable knowledge (candidate for 0.6)

- **Review reminders:** knowledge types, responsible people and review dates are built ([design](docs/design/knowledge-metadata.md)); overdue reviews show in entries, `list_knowledge review=due` and `whats_new`. Still open: telling responsible people and curators actively when a review falls due (needs the notifications below), and recording the last review as its own date.
- **Systematic permission tests:** the matrix of roles (outsider, reader, author, curator, space admin, instance admin) across tools and space kinds is built for knowledge ([`permission_matrix_test.go`](internal/mcp/permission_matrix_test.go)), for skills ([`permission_matrix_skills_test.go`](internal/mcp/permission_matrix_skills_test.go)) and for the administration tools ([`permission_matrix_admin_test.go`](internal/mcp/permission_matrix_admin_test.go): all `admin_*` tools, and the space admin tools in their own, a foreign and an archived space): every cell calls the real tool and compares with the rule, and callers who may not see a target must get the same answer as for one that does not exist. Still open: sending (`share_*`), the skill export route, the edition tools, and the cells for search excerpts, versions and counts.
- **Rights report for instance admins:** a read-only `admin_*` tool that lists who holds which role where (space admins, default roles, spaces open to everyone, instance admins), so an operator can check the configuration of a running instance. The permission matrix proves the code, this shows the setup.
- **Summarising with `replaces`:** `store_knowledge` gets a `replaces` field (ids of sources in the same space) and `confirm=true`. The summary is stored and the sources are archived in the same transaction. Provenance is recorded as `derived_from`, and the sources carry the note "summarised in ...". Whoever may not archive the sources stores the summary as a draft; the curator archives the sources when releasing it, and a source that changed since the request is left alone. Replacing across spaces is deliberately not supported.

### Sending to a colleague, follow-ups

Sending an entry or a skill to a colleague's personal space is built ([docs/sharing.md](docs/sharing.md)). Still open: a notice to the recipient when something arrives (needs the notifications above), the sender seeing the outcome and recalling an item that is still waiting (needs a small table of sent items, which the first version avoids on purpose), a switch for a single user to refuse items, and sending to a group of people or a team space (which would be an ordinary draft in that space, not a personal one).

### Security, remainder of the audit of 2026-11-03

- consent page before a new client is connected
- audit log: an optional hash chain over the entries, so that a gap or a change is detectable. The copy on stdout, the retention period and the append-only server account exist.
- abuse detection (unusually many reads, failed token checks) in `admin_health`

### Editions and release

Community (this repository, up to 8 active accounts) and Pro are built from one code base ([docs/licensing.md](docs/licensing.md)).

### Tool list after a change of rights (open gap)

Clients read the tool list once per connection and do not reliably refresh it. A user who becomes curator or space admin may therefore keep seeing the compact form of the curator and space admin tools until the connector is reconnected. Only the listed tools and their descriptions are stale: rights are checked on every call. Atrium marks its answers as not cacheable (`ttlMs: 0`, scope `private`) and sends no `notifications/tools/list_changed`. In a measurement with Claude Code 2.1.321 no refresh followed a change of rights within five minutes; one reload came after exactly six minutes, for a reason not yet established. Unsolved. Options: send list-changed notifications over `subscriptions/listen` (needs a long-lived server and a registry of open streams per user instead of one server per request), after finding out which clients act on them.

### Documentation and operations

- The user guides in `docs/user/` exist (Markdown and PDF). Still to do after the rollout: adapt the manual in the space "Allgemein" to them and to the archive behaviour, and set the description of "Allgemein" and the instance context.
- Make the language of server messages configurable, if customers ask for it. English is the default and only language for now. Comments in the code that refer to a "concept" document by section number should be replaced by a self-contained explanation.

## Skill registry (strategic)

Atrium becomes the controlled registry and distribution service for company-wide skills (the open [Agent Skills](https://agentskills.io) format: a `SKILL.md` with metadata, instructions and optional references, assets and scripts). Spaces, roles, review, versioning and audit are reused. The idea comes from a ChatGPT test against a demo server; the assessment below is ours.

**Why it fits, and the one rule that matters.** A skill is deliberately an instruction to an assistant. Knowledge entries are untrusted data and must never steer an assistant (the reason for the untrusted-content notices). The two must therefore never share a trust path:

- Skills are a separate kind with their own tools. `get_knowledge` stays pure data retrieval, and an ordinary entry can never become a skill by what it says.
- Atrium decides which concrete skill version, after which checks, may be offered to which users. The client decides whether to activate it and which tools, files and network access it allows. A declared `allowed-tools` is a request, not a permission. Atrium cannot enforce what a foreign client does and must not pretend to.
- Publishing a knowledge entry does not grant the right to distribute skills. The admin right for entries is not carried over to skills without a separate decision, and self-approval of skills is restricted.

**Data model from day one** (so that distribution can change later without migrating): a skill is a versioned package with its own identity, owner, space and status, not a Markdown field.

- Releases are immutable and addressed by version and manifest digest (sizes and SHA-256 per resource). A change is a new version with a new review. A hash proves the file matches the approved manifest, not that it is harmless.
- Resources have safe relative paths (no traversal, no symlinks). Scripts and assets are allowed in the model but forbidden in the first version.
- "Approved" and "published" are separate states, and a release can be blocked. Review, publication and activation are decoupled.
- One transport-neutral service ("skill X, version Y, resource Z") checks space rights, status and integrity on every list, manifest, file and old-version read. Export, sync and MCP endpoints all use it.
- Audit covers create, change, review, approve, publish, read, block and export. Actual execution can only be observed if clients report it.

**Review.** The existing draft and curator review is reused and extended: technical check of the whole package (format, references, paths, requested capabilities), subject approval by a curator, and for riskier skills a security approval by a separately authorised role. Risk classes are Atrium's own proposal, not part of the standard: A instructions only, B tool use, C critical actions (deploy, delete, send data out). The class must not rely on what a `SKILL.md` says about itself.

**Phases**

1. **Text skills over plain MCP tools (implemented, see [docs/skills.md](docs/skills.md)).** The skill model above, with the tools `store_skill`, `list_skills`, `get_skill`, `get_skill_resource`, `review_skill`, `publish_skill`, `block_skill`, `unblock_skill`, `archive_skill`, `unarchive_skill` and `delete_skill`. They work in every MCP client, so a skill stays usable where a client does not know the skills extension (graceful degradation). Published skills are announced to the assistant in the per-user instructions. `SKILL.md` and referenced text files only, and Atrium never runs anything. Includes the negative tests: skill prompt injection, unauthorised listing and reading, tampered packages, old and blocked versions, traversal, malformed manifests, privilege escalation.
2. **Standard distribution over MCP (extension and export implemented, see [docs/skills.md](docs/skills.md)).** The MCP Skills extension (SEP-2640: `skills/list`, `skills/get`, files as `skill://` resources) on top of the same internal service, and a ZIP export of a released version (`GET /skills/export/...`). Still open: test Claude Code, ChatGPT and Codex for real before claiming compatibility (the extension does not depend on it, the tools remain the fallback), and a local sync adapter for clients that prefer installed skills. The adapter needs a way for a command line tool to obtain a token, and the update and revocation rules from phase 3.
3. **Richer packages.** Scripts and assets, dependencies, risk-based security review, policy-controlled updates and revocation, optional usage tracking with data protection in mind.

**Installation versus loading at runtime.** Phase 1 loads at runtime through the tools, which gives the strongest control: every fetch passes the server, so pinning, revocation and audit hold without local copies. Installation (export, sync) comes in phase 2 and keeps the same rules: a client pins a concrete version and never switches silently (the default of the tools is only a lookup aid); a running task keeps its version; revocation cannot recall local copies without client cooperation, so document expiry and rollout rules.

**Limits of the tool path, to be said openly.** The assistant has to be told that skills exist (done through the instructions, capped to a few entries). Models usually treat tool results as data, so the tool descriptions state that a skill is followed only when the user asks for it, and results carry origin, version and digest but no rights. How reliably a given client follows such a skill must be tested per client. Scripts cannot run in a plain chat client, which is why phase 1 is text only.

**Done when** a published version can be exported and later read remotely with identical resources and hashes, a published release never changes, an unauthorised client sees neither lists nor files, a blocked version is refused on new fetches, and no tool call gains rights from skill content alone.

## Later

- **Roles for customers with high protection needs:** instance admins are admin in every team space today and can read everything there. Some customers need a split between "manage users and operations" and "read content" (for example for management or HR). This includes separate roles, a waiting period with a second admin when transferring personal spaces, and notifications. Personal spaces are already closed to everyone except their owner.
- **Review notifications:** curators already find waiting drafts with `list_knowledge status=draft` and see the counts of all their spaces in `whats_new`; authors learn of approvals and returns only by looking. Start with a review queue and an optional digest, later e-mail and chat integrations. Notifications must not carry metadata to people who may not see the entry.
- **A draft next to the released version:** authors of spaces that require approval cannot change approved entries today.
- **Correction reports:** readers flag an entry as wrong, incomplete, unclear or outdated, with a reason and the place concerned. The owners get a reviewable item; reports never overwrite binding knowledge directly.
- **Citations:** stable, resolvable references that name the version used and the section (the heading path is already stored per chunk and returned by search, the document version is not yet part of a hit), also for older versions and direct links, with the same rights checks. An answer should say "policy X, version 4, section 3.2".
- **Contradiction hints:** find semantically related entries and show a curator likely conflicts (for example two different hotel limits) with the entries and sections concerned. The assistant does the comparing on request, in the same way as summarising; the server stays free of a model of its own. Nothing is changed automatically, because context such as date or location may explain a difference. Duplicate detection at save time exists already.
- **Import:** Markdown and structured data with titles, tags and provenance, for migration from wikis. Mind rights, duplicates and stable links. Export is built: admins export spaces and the whole instance, everybody their personal space, as Markdown files (`export_knowledge`). Not part of it yet: older versions of an entry, skills in bulk and the audit log.
- **Search quality:** a test corpus of questions with expected references, run regularly against ranking, recall, synonyms, chunk boundaries, embeddings, rights filters and stale hits. It shares its tooling with the load test below. With it, compare smaller or newer embedding models with bge-m3 on German content (candidates: Qwen3-Embedding-0.6B, Apache 2.0; EmbeddingGemma-300m, Gemma terms and a gated download) for hits, speed on 2 cores and memory; `admin_reembed` makes a switch possible.
- **Relations between entries:** "see also", "supplements", "replaces", "requires", "conflicts with". Assistants may suggest, people confirm. `derived_from` from the summarising feature becomes one relation type. Relations survive moving an entry.
- **Skills and knowledge side by side:** both content types should offer the same functions wherever the difference in trust allows it. Skills now have archive, restore, delete, unblock, tags, filters (`tag`, `query`, `status`) and quotas. Moving a skill (`move_skill`, reviewed again in the target space) and the audit filters by skill and client are done. Still missing: searching skills by meaning (embeddings), reading an archived skill before restoring it, a duplicate warning, and the strict space rule for `replaces` between skills. Open question: whether a space admin may delete skills that never left draft, rejected or approved (nothing was ever served).
- **Server support for the shipped knowledge skills** (`skills/knowledge-ingest`, `skills/knowledge-lint`): a tool that lists the lint candidates of a space (review due, very similar pairs of entries, drafts waiting for long, entries without tags), one that finds the related entries for a long source section by section, and `list_knowledge` paging or sorting by age, so that a lint reaches the oldest entries of a space with more than 100. Deterministic and rights-checked; the reading and writing stays with the user's assistant.
- **Knowledge gaps:** aggregate frequent searches without a hit into proposals for missing entries. This conflicts with the current decision not to log search texts, so it needs an explicit opt-in, aggregation and no link to a person; it never publishes invented answers.
- **Usage statistics:** aggregated frequent questions, unused entries, searches without hits, open approvals and due reviews, strictly without watching individual employees.
- **Groups from the identity provider:** map groups to spaces, with protection against stale groups (never more than reader through groups, reconciliation at every login, visibility for admins).
- **Client ID Metadata Documents:** the client registration the OAuth specification prefers. Dynamic Client Registration works with Claude and ChatGPT today.
- **Reranker** for better search results (the configuration section exists, the code does not).
- **Privacy:** tool to anonymise users in the audit log.
- Test **Entra login** against a real tenant and **Bedrock** against a real AWS account.

## Scaling (not measured yet)

Statements about data volume and performance are estimates so far. Before making firm promises to customers:

1. Load and multi-user test with synthetic corpora (100k, 1M, 10M chunks) and permission filters of varying selectivity, above all for filtered vector search, with parallel search, read, write, approve, archive, rights changes and sessions. Measure latency, errors, index consistency, background jobs and race conditions.
2. Remove the bottlenecks of running several instances: role cache and rate limits per process (replace with `LISTEN/NOTIFY` and a shared counter), periodic jobs on one instance only (leader lock), separate pools for reads and writes, asynchronous audit, a cache for query vectors.
3. Operations guide for read replicas, load balancer and Postgres cluster.
4. Regular compatibility runs against ChatGPT, Claude and later clients: tool discovery, OAuth, timeouts, aborts, retries, error codes, and relevant changes of the MCP specification.

## Already covered or deliberately not planned

- **Assistant proposes entries from a conversation:** already how Atrium works. The assistant stores only on the explicit request of the user, `draft=true` sends it to a curator, and similar entries are checked before saving. No further feature planned.
- **Several customers on one instance.** An instance belongs to one organisation; separate customers get separate instances.
- **Heuristic detection of prompt injection** in the store. It would give false security. Instead, content is marked as data and risky actions need the user's confirmation. For skills, the answer is the separate trust path above, not a filter.
