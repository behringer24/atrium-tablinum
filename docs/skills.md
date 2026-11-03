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
  └─> rejected       └────── block ─────────┘──> blocked
```

- **draft:** saved with `store_skill`. An author who saves again while their own draft is still unreviewed replaces that draft; otherwise a new version number is created.
- **approved:** a curator (or space admin, or instance admin) has read the files and approved them with `review_skill`, naming a **risk class**: A instructions only, B uses tools, C critical actions (deploy, delete, send data out). The class is the reviewer's judgement of the real content, not of what the skill says about itself. A skill that requests tools cannot be class A.
- **published:** `publish_skill` makes the version available to all readers of the space. Class C can only be published by an instance admin. Before publishing, the stored files are hashed again and compared with the digest.
- **rejected:** the curator returned it with a note the author sees.
- **blocked:** `block_skill` withdraws an approved or published version at once, with a reason. Blocking cannot be undone; a fix is a new version with a new review.

A published version **never changes**: the database refuses updates and deletes of its files and content, and a state cannot go back to draft. A change is a new version that passes the review again.

Approved and published are separate on purpose: a review says "this is fine", publishing says "offer it now".

### Who may do what

| | Reader | Author | Curator, space admin | Instance admin |
|---|---|---|---|---|
| Load released skills (`list_skills`, `get_skill`, `get_skill_resource`) | yes | yes | yes | yes |
| See drafts, rejected and blocked versions | no | own | all in the space | all in team spaces |
| `store_skill` | no | yes (draft) | yes (draft) | yes (draft) |
| `review_skill`, `publish_skill`, `block_skill` | no | no | yes | yes |
| Publish a class C skill | no | no | no | yes |

- **No self-approval:** the author of a version cannot approve it, not even as curator or admin; another curator has to. This is stricter than for knowledge entries on purpose. Small installations with a single curator can set `skills.allow_self_approval: true` in `config.yaml`.
- **Personal spaces:** the owner saves and publishes in one step. Nobody else can ever see a personal skill, instance admins included.
- Rights are checked on every call, also for old versions and single files. What the caller may not see is reported as "not found".

## Tools

| Tool | Who | Purpose |
|---|---|---|
| `list_skills` | everyone | Skills the caller can use (released versions); with `all_versions=true` also the drafts and blocked versions the caller may see |
| `get_skill` | everyone | `SKILL.md`, file list with hashes, version, digest, risk class. Without a version: the newest released one |
| `get_skill_resource` | everyone | One file of a skill, checked against its hash |
| `store_skill` | authors and up | Saves the package as a new version or revises the author's own draft |
| `review_skill` | curators | Approve (with confirmation and risk class) or reject (with note) |
| `publish_skill` | curators | Release an approved version (confirmation required) |
| `block_skill` | curators | Withdraw a version (reason and confirmation required) |

All of them work in every MCP client. A client that does not know any skills extension can still use a skill: the assistant loads it through the tools. Released skills are announced in the instructions each connection receives (name, space, description, at most 25 entries), because the assistant would not know otherwise that they exist. It is told to use one only when the user asks or agrees.

To stay on the version it started with, an assistant passes the exact version to `get_skill` and `get_skill_resource`. The default (newest released version) is only a lookup aid.

## Audit

`store_skill`, review, publish and block are written to the audit log (`skill_save`, `skill_approve`, `skill_reject`, `skill_publish`, `skill_block`), and every read (`skill_get`, `skill_resource`) with skill name and version. In personal spaces only action and time are kept.

## Limits to know

- Blocking stops new reads. A client that already holds a copy keeps it until it fetches again; Atrium cannot recall it.
- How reliably a given assistant follows a skill it loaded through a tool is up to the client and has to be tested per client. Models usually treat tool results as data, which is why the tool descriptions and notices say when a skill is to be followed.
- Skills have no search: find them with `list_skills` or the instructions.
- Planned next: the MCP Skills extension (SEP-2640) on the same service, export and sync for clients that install skills, and scripts. See the [roadmap](../ROADMAP.md).
