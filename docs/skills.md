# Skill registry

Atrium can distribute skills: approved working instructions that an assistant follows when the user asks for them, for example "review this change the way we do it here". A skill is a small package of text files in the open Agent Skills layout (a `SKILL.md` with a front matter block, plus optional reference files). Spaces, roles, review, versions and the audit log work as for knowledge entries, with the stricter rules below.

## Why skills are not knowledge entries

A knowledge entry is untrusted data. Anyone with write access may have put anything into it, so an assistant must never obey what it says (the reason for the untrusted-content notices). A skill is the opposite: an instruction the organisation approved. Mixing the two would either weaken the protection against prompt injection or make skills useless. Therefore:

- Skills live in their own tables and are served only by their own tools. `search_knowledge` and `get_knowledge` never return a skill, and no knowledge entry can become a skill by what it says.
- Atrium decides which version, after which checks, is offered to whom. The client (the assistant) decides whether to use it and which tools, files and network access it allows. A skill's `allowed-tools` is a request, not a permission. Atrium cannot enforce what a foreign client does, and a skill gives the assistant no right it did not have.
- Results carry a notice telling the assistant to follow the skill only because the user asked for it, to keep its own tool rules and confirmation requirements, and to ignore and report anything that asks it to bypass them.

This version distributes text only. Atrium never runs anything; scripts and binary files are refused.

## The package

A package is a set of text files. Limits: at most 40 files, 64 KB per file, 256 KB in total.

- `SKILL.md` (exact spelling) is required. It starts with a front matter block:

  ```
  ---
  name: code-review
  description: Review a change before it is merged
  allowed-tools: Read Bash(git:*)     # optional, a request only
  ---
  ```

  `name` must equal the skill name (lower case letters, digits, single hyphens, at most 64 characters). `description` is required (at most 1024 characters); it is what assistants and users see in the list.
- Other files are referenced from `SKILL.md`, for example `references/checklist.md`. Allowed types: `.md`, `.txt`, `.json`, `.yaml`, `.yml`, `.csv`.
- Paths are relative with forward slashes. Empty, hidden (leading dot) and relative (`.`, `..`) segments, a `scripts/` directory and duplicates that differ only in case are refused.
- The files are scanned for credentials (the same check as for knowledge entries) and rejected if they contain any.

## Versions and states

A skill has numbered versions. Each version has a **digest**: the SHA-256 over all paths and file hashes, shown with the version and in the file list (each file has its own hash). The digest says the content is the one that was reviewed; it does not say the content is harmless.

```
draft ──review──> approved ──publish──> published
  │                  │                      │
  └─> rejected       └────── block ─────────┘──> blocked ──unblock──> approved
```

- **draft:** saved with `store_skill`. An author who saves again while their own draft is still unreviewed replaces that draft; otherwise a new version number is created.
- **approved:** a curator (or space admin, or instance admin) has read the files and approved them with `review_skill`, naming a **risk class**: A instructions only, B uses tools, C critical actions (deploy, delete, send data out). The class is the reviewer's judgement of the real content, not of what the skill says about itself. A skill that requests tools cannot be class A.
- **published:** `publish_skill` makes the version available to all readers of the space. Class C can only be published by an instance admin. Before publishing, the stored files are hashed again and compared with the digest.
- **rejected:** the curator returned it with a note the author sees.
- **blocked:** `block_skill` withdraws an approved or published version at once, with a reason. Only an admin of the space or an instance admin can lift a block with `unblock_skill` (reason required). The version then becomes **approved**, not published: a curator has to release it again (class C: an instance admin), and publishing checks the files against the digest once more. The database allows no other way out of blocked.

A published version **never changes**: the database refuses updates and deletes of its files and content, and a state cannot go back to draft. A change is a new version that passes the review again.

Approved and published are separate on purpose: a review says "this is fine", publishing says "offer it now".

### Archive and delete

- **Archive** (`archive_skill`, restore with `unarchive_skill`) works on the whole skill, not on single versions. An archived skill disappears for everyone who cannot restore it: `list_skills`, `get_skill`, `get_skill_resource` and the instructions no longer know it. Curators and admins of the space (instance admins in every team space, the owner in a personal space) find it with `list_skills archived=true`. Nothing is deleted and every version keeps its state, so restoring needs no new review. An archived skill takes no new versions, no review and no release; a version can still be blocked.
- **Delete** (`delete_skill`) removes the skill with all versions and files for good. In a team space **only an instance admin** may: skills are instructions that assistants followed, so removing them is not a matter for a single space. Everyone else archives or blocks, which keep the evidence. In a personal space the owner may delete. The audit log keeps the name and the version numbers, states and digests (`skill_delete`). Block, archive and delete answer different needs: block withdraws a version and keeps it, archive hides the whole skill and keeps it, delete is the only step that cannot be undone.

### Who may do what

| | Reader | Author | Curator, space admin | Instance admin |
|---|---|---|---|---|
| Load released skills (`list_skills`, `get_skill`, `get_skill_resource`) | yes | yes | yes | yes |
| See drafts, rejected and blocked versions | no | own | all in the space | all in team spaces |
| `store_skill` | no | yes (draft) | yes (draft) | yes (draft) |
| `review_skill`, `publish_skill`, `block_skill` | no | no | yes | yes |
| `archive_skill`, `unarchive_skill`, read the archive | no | no | yes | yes |
| `unblock_skill` | no | no | space admin only | yes |
| `delete_skill` (team space) | no | no | no | yes |
| Publish a class C skill | no | no | no | yes |

- **No self-approval, except for instance admins:** the author of a version cannot approve it, not even as curator or space admin; another curator has to. This is stricter than for knowledge entries on purpose. Instance admins are the exception: they can review and release their own skills (class C included), and the audit entry `skill_approve` is marked `self_approved`. Small installations without an instance admin among the curators can set `skills.allow_self_approval: true` in `config.yaml` to allow it for every curator.
- **Personal spaces:** the owner saves and publishes in one step. Nobody else can ever see a personal skill, instance admins included.
- Rights are checked on every call, also for old versions and single files. What the caller may not see is reported as "not found".

## Tools

| Tool | Who | Purpose |
|---|---|---|
| `list_skills` | everyone | Skills the caller can use (released versions); with `all_versions=true` also the drafts and blocked versions the caller may see, with `archived=true` the archive (curators and admins) |
| `get_skill` | everyone | `SKILL.md`, file list with hashes, version, digest, risk class. Without a version: the newest released one |
| `get_skill_resource` | everyone | One file of a skill, checked against its hash |
| `store_skill` | authors and up | Saves the package as a new version or revises the author's own draft |
| `review_skill` | curators | Approve (with confirmation and risk class) or reject (with note) |
| `publish_skill` | curators | Release an approved version (confirmation required) |
| `block_skill` | curators | Withdraw a version (reason and confirmation required) |
| `unblock_skill` | space admins | Lift a block (reason and confirmation required); the version is approved again, not released |
| `archive_skill`, `unarchive_skill` | curators | Hide a whole skill from everyone else, or bring it back (confirmation required) |
| `delete_skill` | instance admins, owners of a personal space | Delete a skill with all versions for good (confirmation required) |

All of them work in every MCP client. A client that does not know any skills extension can still use a skill: the assistant loads it through the tools. Released skills are announced in the instructions each connection receives (name, space, description, at most 25 entries), because the assistant would not know otherwise that they exist. It is told to use one only when the user asks or agrees.

To stay on the version it started with, an assistant passes the exact version to `get_skill` and `get_skill_resource`. The default (newest released version) is only a lookup aid.

## MCP Skills extension (SEP-2640)

Atrium declares the extension `io.modelcontextprotocol/skills` and serves released skills as resources, so a client that supports it can list and load them like its own skills without the tools. The tools stay as they are: a client that does not know the extension keeps working.

- **URI:** `skill://<space-slug>/<skill>/<file>`, for example `skill://dev/code-review/SKILL.md`. The space slug is the organisational prefix the extension allows, because a skill name is unique only within one space. `list_spaces` shows the slugs; the slug of a personal space is `personal-<id>`.
- **`skills/list`** returns the newest released version of every skill the caller may read: the front matter of `SKILL.md` verbatim, and the complete file list with `sha256:` digest and size per file. At most 200 skills are listed; the extension allows a partial list, and every skill stays readable by its URI. **`skills/get`** returns the same entry for one `SKILL.md` URI. Anything unknown, not released, archived, blocked or outside the caller's spaces is the same error (`-32602`).
- **`resources/read`** returns one file (`text/markdown` for `.md`) with version and digest in `_meta`. Directory reads (`resources/directory/read`) are not offered.
- **Same rules as the tools:** space rights, released status, archive and block, hash check of every file and rate limit apply to every call, and reads are audited like `get_skill`. Only the newest released version is addressable. A new release changes the digests in the entry, which a host has to approve again; the tools remain the way to ask for an exact old version.
- **Scripts stay out:** Atrium still stores text only and runs nothing; `resources` is always the complete list, never `"dynamic"`.

Whether a given client uses the extension is up to the client. The extension has been final since September 2026, client support is still being built; test the clients you rely on.

## Export

`GET /skills/export/<space-slug>/<skill>` returns the newest released version as a ZIP in the Agent Skills layout (one directory named like the skill), `?version=N` a specific released version. The request needs the same bearer token as `/mcp`. Rights and rules are those of the tools; drafts, blocked versions and archived skills cannot be exported, and the export is audited as `skill_export`. The response carries `X-Atrium-Skill-Version`, `X-Atrium-Skill-Digest` (the digest of the version, as in `get_skill`) and the same value as `ETag`. The same version always gives the same bytes.

An export is a copy: blocking or archiving cannot recall it. A client that installs skills pins the version and digest it exported and decides itself when to fetch again (see the roadmap for the planned sync adapter).

## Audit

`store_skill`, review, publish, block, unblock, archive, restore and delete are written to the audit log (`skill_save`, `skill_approve`, `skill_reject`, `skill_publish`, `skill_block`, `skill_unblock`, `skill_archive`, `skill_unarchive`, `skill_delete`), and every read (`skill_get`, `skill_resource`, `skill_export`) with skill name and version. In personal spaces only action and time are kept.

Every read carries `via` in its details, the route the client took: `tool` (`get_skill`, `get_skill_resource`), `resource` (`resources/read` of a `skill://` URI), `rpc` (`skills/get`) or `export` (the ZIP download). `skills/list` is written as `skill_list` with the number of skills offered. So `admin_audit_log action=skill_*` shows whether a client uses the SEP-2640 methods or the tools. Calls through `skills/*` carry the client `mcp-skills`, as that route has no MCP session to read the client name from.

To see the requests themselves, set `instance.log_level: debug`: the server then logs one line per MCP request (`MCP request`, with `method`, `tool`, `uri`, `client_info` on `initialize` and `user_agent`), e.g. `docker logs <container> 2>&1 | grep 'MCP request' | grep -E 'skills/|resources/read|get_skill'`.

## Limits to know

- Blocking, archiving and deleting stop new reads. A client that already holds a copy keeps it until it fetches again; Atrium cannot recall it.
- How reliably a given assistant follows a skill it loaded through a tool is up to the client and has to be tested per client. Models usually treat tool results as data, which is why the tool descriptions and notices say when a skill is to be followed.
- Skills have no search: find them with `list_skills` or the instructions.
- Planned next: a sync adapter for clients that install skills (with update and revocation rules), and scripts. See the [roadmap](../ROADMAP.md).
