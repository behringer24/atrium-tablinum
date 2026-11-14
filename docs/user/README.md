# Atrium user guides

Atrium Tablinum (short: **Atrium**) is your organisation's shared memory for AI assistants: a place where knowledge and approved working instructions ("skills") are kept, found and reused by Claude, ChatGPT and other assistants that speak the Model Context Protocol (MCP). These guides explain how to work with it, one guide per group of people.

You do not operate Atrium through a screen of its own. You talk to your AI assistant ("Search Atrium for our expense policy", "File this in the Support space") and the assistant uses Atrium's tools on your behalf. The guides therefore show what to say, what happens behind the scenes, and which tool is involved when you need to look something up or report a problem.

## Which guide is for me?

| Guide | For | You will learn |
|---|---|---|
| [Why Atrium](00-why-atrium.md) | Everybody, and anyone who still needs convincing | What Atrium brings you, your team and the organisation, a day with it, your first ten minutes, and the honest limits. A good document to share first |
| [Getting started](00-getting-started.md) | New users | Connect your assistant, ask a first question, save a first note and find it again in a new chat, in about five minutes. The first guide to read |
| [1. Everyday use](01-user.md) | Everybody who has an Atrium login | Connect an assistant, find knowledge, use skills, work in your personal space, hand work over to another assistant, stay safe |
| [2. Authors](02-author.md) | People who may write in team spaces | File and maintain knowledge, work with drafts and approval, write and propose skills |
| [3. Curators](03-curator.md) | People who review and approve | Review knowledge and skills, judge risk, publish, block, keep a space tidy |
| [4. Space admins](04-space-admin.md) | People who manage one space | Members, roles and groups, access reviews, unblocking, deleting |
| [5. Instance admins](05-instance-admin.md) | The people who run the Atrium instance | Spaces, users, offboarding, login rules, context, audit, health, security and operations |
| [Atrium Pro](../pro/pro-edition.md) | Instance admins (and space admins) of an organisation with a Pro licence | Install and renew the licence, updates with a licence, the user limit, guided offboarding |

Each guide builds on the one before it: a curator is also an author and a reader, a space admin is also a curator, and so on. Read the guides in order if you are new to a role. Every guide ends with a short FAQ.

## Roles at a glance

Access is organised in **spaces**. A space is a collection of knowledge entries and skills with its own members. There are two kinds:

- **Team spaces** belong to the organisation. Members have one of four roles in each space.
- **Personal spaces** belong to one person. Only the owner can read them, not even instance admins.

| Role in a space | Can do |
|---|---|
| **Reader** | Search, read and list approved knowledge; use released skills |
| **Author** | Everything a reader can, plus file knowledge, change and move their own entries, propose skills |
| **Curator** | Everything an author can, plus review drafts and skills, change entries of others, publish and block skills |
| **Admin** (space admin) | Everything a curator can, plus manage the members of the space, lift skill blocks, delete entries for good |
| **Instance admin** | Admin in every team space, plus the `admin_*` tools to create spaces, manage users, logins and the audit log. Cannot read personal spaces |

The role you have can differ from space to space. Your assistant can show your roles with the tool `list_spaces`, and `space_contacts` tells you who the curators and admins are.

> **Rights, precisely.** "Space admin" and "instance admin" are different. A space admin manages one space (`space_members`, `space_add_member`, `space_remove_member`, `unblock_skill`, `delete_knowledge`). An instance admin counts as admin in every team space and additionally has the `admin_*` tools. Sources in the code: `internal/authz`, `internal/docs/write.go`, `internal/skills/skills.go`.

## Words used in the guides

| Term | Meaning |
|---|---|
| **Entry** (knowledge entry) | A piece of knowledge in Markdown with a title, tags and a version history. Data, never an instruction |
| **Skill** | An approved working instruction for an assistant: a `SKILL.md` plus optional text files. Immutable once released |
| **Draft** | An entry or skill version waiting for review. Visible only to its author and the curators of the space |
| **Approved / released** | Reviewed and visible to all readers of the space |
| **Handoff** | A short summary stored in your personal space so you can continue in another assistant |
| **Type** | What an entry is: `rule` (binding, set by curators only), `documentation`, `recommendation`, `experience` |
| **Responsible person** | Who answers for an entry, if somebody named one; marked `former` after they left |
| **Review date** | The day by which an entry should be checked; an overdue review is shown, nothing is hidden |
| **Digest** | A SHA-256 fingerprint of a skill version. Reviewers approve a specific digest |
| **Instance** | One running Atrium for one organisation |

## About these documents

- Markdown (`.md`) is the source. A PDF with the same name sits next to every guide for printing and offline reading.
- The guides describe the behaviour of the current development version. Features that are planned but not built are marked as such, and the [roadmap](../../ROADMAP.md) lists what is coming.
- Organisations with a Pro licence also read [Atrium Pro](../pro/pro-edition.md) (licence and guided offboarding, in `docs/pro/`).
- Technical reference for operators lives in the other documents of this repository: [skills](../skills.md), [approval](../approval.md), [archive and context](../archive-and-context.md), [offboarding](../offboarding.md), [login and OAuth](../auth.md), [deployment](../deployment.md), [embedding](../embedding.md), [releases](../release.md).

Every PDF carries the same branding: the Atrium icon and the behringer24 logo in a header block on the first page, a running header on every other page, and a footer with the names and the page number. To rebuild the PDFs after editing a guide, run `scripts/build-docs.sh` (needs Node.js and Chrome, Chromium or Edge); pass the changed file to rebuild only that one.
