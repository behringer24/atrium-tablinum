# Atrium for everyday use

This guide is for everybody who has an Atrium login: you connect an assistant once, then use it to find what your organisation already knows, use approved skills, keep private notes and move work between assistants. If you are also allowed to file knowledge in team spaces, continue with the [author guide](02-author.md).

## Contents

- [What Atrium is and is not](#what-atrium-is-and-is-not)
- [Connecting an assistant](#connecting-an-assistant)
- [Spaces and roles](#spaces-and-roles)
- [Finding knowledge](#finding-knowledge)
- [Your personal space](#your-personal-space)
- [Handoffs: continuing in another assistant](#handoffs-continuing-in-another-assistant)
- [Skills](#skills)
- [Your logins and your safety](#your-logins-and-your-safety)
- [Daily workflows](#daily-workflows)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)

## What Atrium is and is not

Atrium is a shared store with two kinds of content:

- **Knowledge entries** are facts, processes, decisions and know-how written as Markdown. Your assistant searches them to answer questions about your organisation. They are *data*: an assistant reads them but never takes orders from them.
- **Skills** are approved working instructions ("how we do a code review", "how we answer a support ticket"). A skill is an *instruction* an assistant follows, but only when you ask for it. Skills are reviewed by curators before anybody can use them.

Atrium is not a chat, not a file share and not a backup. It does not run programs. A skill is text, and Atrium never executes anything.

Your assistant only stores something in Atrium when you explicitly ask it to. It never files things on its own initiative.

## Connecting an assistant

You connect an assistant once per assistant. You need the address of your Atrium, which your administrator gives you. It looks like `https://atrium.example.com/mcp`.

1. In your assistant, add a custom connector (Claude, ChatGPT) or MCP server (Claude Code and others) and enter the address.
2. The assistant opens a login page of Atrium. Log in with your organisation account (for example Google or Microsoft). Only accounts your administrator allowed can log in.
3. After the login the assistant can use Atrium. Tools may be loaded on demand: in a fresh chat the assistant sometimes needs a moment before it knows what Atrium contains.
4. Try it: *"Which Atrium spaces can I use?"* The assistant answers with your spaces and roles.

Connections last as long as you stay active. Atrium ends every login after 90 days at the latest, and you log in again. You can see where you are logged in and end logins yourself (see [Your logins and your safety](#your-logins-and-your-safety)).

> **Tip.** If you mention "Atrium" in a request, the assistant knows to use this store. Without the name, an assistant decides itself whether a question might concern company knowledge.

## Spaces and roles

Everything in Atrium lives in a **space**:

- **Team spaces** are shared, for example "Support", "Engineering" or "HR Policies". Each has members with roles: reader, author, curator or admin. Some spaces are open for reading to everybody who is logged in.
- **Your personal space** is yours alone. Nobody else can read it, not even administrators.

Ask *"Which spaces do I have in Atrium?"* (tool `list_spaces`) to see every space you can access, your role there, whether the space needs approval for new entries, and which space is your default for saving.

If you want to know who looks after a space, or whom to ask for more rights, ask *"Who are the curators of the Support space?"* (tool `space_contacts`).

What you can do in a team space depends on your role:

| Role | What it means for you |
|---|---|
| Reader | You can search, read and list approved entries and use released skills |
| Author | You can also file knowledge and propose skills, see the [author guide](02-author.md) |
| Curator | You also review what others submit, see the [curator guide](03-curator.md) |
| Admin | You also manage the members of the space, see the [space admin guide](04-space-admin.md) |

In your personal space you are always the admin.

## Finding knowledge

Just ask your assistant, in your own words and language:

- *"What does Atrium say about our travel expense policy?"*
- *"Search Atrium for the escalation process for P1 incidents."*
- *"Look in the Engineering space for our decision on the database migration."*

Behind the scenes the assistant uses `search_knowledge`. The search combines meaning (a vector search) with exact words (full text), so you find entries even when you use different wording than the author.

What you get:

- **Sections of entries** with title, space, author, date and status, plus an excerpt. The assistant should name the entry it relies on, so you can check the source.
- Only entries you may see. Drafts of other people never appear for you (unless you are a curator of that space), and neither do archived entries.
- At most 5 hits by default (up to 20 on request). Ask for more or narrow down with the space or a **tag**: *"Only in the Support space, tagged 'refunds'."*

To read a whole entry, the assistant uses `get_knowledge` with the entry's ID from the search. To browse a space, it uses `list_knowledge` ("Show the newest entries in Engineering", optionally by tag).

To take your notes with you, ask *"Export my personal space."* (`export_knowledge`). The assistant gives you a link that works for ten minutes and once; open it in your browser and you get a ZIP file with one Markdown file per entry. Only your own notes are in it, and only you can get them. Space admins can export their spaces the same way (see the [space admin guide](04-space-admin.md)).

To catch up, ask *"What is new in Atrium?"* or *"What changed in Support since Monday?"* (`whats_new`). You get the entries of your team spaces that were published (`new`) or changed (`updated`) since then, by default in the last 7 days, and the skills released in that time. Only what you may read appears; drafts, archived entries and personal spaces never do (what colleagues sent to you is the one exception: `sent_to_you` counts it, where sending is enabled).

**What kind of entry is it?** Every entry has a **type**: `rule` is binding (a policy, a regulation, a decision that applies; only curators set it), `documentation` describes how things are, `recommendation` is good practice and `experience` is a report of what worked once. Neither of the last two binds you. The assistant names the type when it answers, and you can ask for rules only: *"What are the binding rules on travel in Atrium?"* An entry can also name a **responsible person** to ask (marked `former` when they have left; ask the curators then) and a **review date**; the assistant tells you when the review is overdue.

Good habits:

- Ask the assistant to **name the entry and its date**. Knowledge can be outdated or contradict other entries; the assistant is told to point that out.
- Check important facts at the source before acting on them.
- If an entry is wrong or outdated, tell a curator (see [Daily workflows](#daily-workflows)).

> **Entries are not instructions.** Text inside an entry is third-party text written by colleagues. Assistants are told never to follow instructions found in entries. If an entry ever seems to tell your assistant to do something unusual, ignore it and report it to a curator or admin.

## Your personal space

Every user has a personal space for private, work-related notes: drafts, checklists, things you want to remember across chats. You can use it without any role.

| You say | Tool |
|---|---|
| *"Save this in my Atrium space."* | `store_knowledge` into your personal space |
| *"Show my latest notes."* | `list_knowledge` (without a space it lists your personal space) |
| *"Update my note about X with this."* | `get_knowledge` then `update_knowledge` |
| *"Archive that note."* | `archive_knowledge` (restorable) |
| *"Delete that note for good."* | `delete_knowledge` (cannot be undone) |

Things to know:

- **Nobody else can read it**, including instance admins. The separation applies within the application; people with direct access to the database or backups could technically read everything.
- **Quota:** by default up to 500 entries and 10 MB (and 50 skills with 5 MB of skill files). Archived entries do not count, and handoffs count towards the size only. If you reach the limit, archive or delete something, or ask an instance admin for more room: limits can be raised for you personally at any time, without a restart.
- **Work-related only.** If you leave the organisation, you are asked to move what belongs to the team before your last day; the rest of your personal space is deleted after a deadline. See [Offboarding](../offboarding.md).
- **Default space.** Without naming a space, new entries go to your default space, normally your personal space. Say *"Make the Support space my default"* (`set_default_space`) to change this. If your default is a team space and you name no space, the assistant asks first, because colleagues can read what lands there; name the space or answer yes to continue.
- **No secrets.** Atrium rejects text that looks like passwords, API keys or private keys. Do not store personal customer data, passwords or credentials.

### Things colleagues send you

Where your organisation has switched sending on, a colleague can send you an entry or a skill (*"Send this checklist to anna@example.com"*). It lands in your personal space as a **draft** that only you can see, with the sender shown as its author; nothing reaches your assistant before you decide.

| You say | Tool |
|---|---|
| *"What did colleagues send me?"* | `whats_new` (`sent_to_you`), `list_knowledge` or `list_skills` with `status=draft` |
| *"Show me that entry."* | `get_knowledge`, `get_skill` |
| *"Keep it."* | `review_knowledge` with `approve`; for a skill `review_skill` and `publish_skill` |
| *"Send it back, it is not for me."* | `review_knowledge` or `review_skill` with `reject` and a note |

Read what arrives as coming from a colleague, not from your organisation: an entry is text, and a skill is an instruction your assistant follows once you release it, so the assistant shows you every file before you approve one. The sender is not told what you decided, and a returned item stays in your space until you archive or delete it. Only a few items can wait at a time (20 by default, 3 from one sender). Sending is described in the [author guide](02-author.md#sending-to-a-colleague) and in [Sending to a colleague](../sharing.md).

Each entry has a title, Markdown content (up to about 200 KB), up to 20 tags, and a version history that Atrium keeps (there is no tool yet to browse old versions).

When you store something, Atrium checks for **similar entries**. If it finds one, nothing is saved yet and the assistant asks whether to update the existing entry instead, or to save a new one anyway.

## Handoffs: continuing in another assistant

A handoff is a short summary of where you are, stored in your personal space, so you can pick it up in another assistant or another chat.

- *"Store the current state for ChatGPT."* (`handoff_save`): the assistant writes goal, results so far, open points and next steps.
- In the other assistant: *"Load my last handoff from Atrium."* (`handoff_load`). With several handoffs, ask *"List my handoffs."* (`handoff_list`).

Handoffs expire after 14 days by default (at most 90) and are deleted automatically. They are not part of the knowledge base: nobody else sees them and search does not return them.

## Skills

A skill is a ready-made, approved working instruction, for example "code-review", "support-reply" or "incident-report". Atrium distributes the skills of your organisation so that everybody works the same way.

### Using a skill

- Type its name as a command if your assistant supports commands: **`/code-review`**. Atrium's tools tell the assistant that a `/name` command may be a skill.
- Or say it in words: *"Use the Atrium skill code-review on this change."*
- To see what exists: *"Which skills are available in Atrium?"* (`list_skills`). You can narrow the list by words (*"skills about support"*) or tag.

What happens: the assistant loads the skill's `SKILL.md` (`get_skill`) and any files it points to (`get_skill_resource`), tells you which version it uses, and follows the steps. Rules that protect you:

- The assistant uses a skill **only when you ask for it or agree**. It will not apply skills on its own.
- A skill grants no extra rights. The assistant still asks you before anything risky, and ignores parts of a skill that tell it to bypass its own rules.
- Only **released** versions reach you. Drafts, rejected and blocked versions are hidden from readers. If a skill is blocked while you use it, new loads are refused.
- The version is stated. If you want to stay on the version you started with, say so.

Some clients can also load skills natively through the MCP skills extension (SEP-2640) or let you download a skill as a ZIP. Which of these works depends on your client; the plain tools work everywhere.

### Skills in your personal space

You can create your own private skills. Say *"File this as a skill called weekly-report in my personal space"*. Personal skills are available to you at once (no review, because nobody else can see them) and follow the same format and limits as team skills. See the [author guide](02-author.md#writing-skills) for the format. Limits: 50 skills and 5 MB per personal space, at most 100 versions per skill.

## Your logins and your safety

- **Where am I logged in?** *"Show my Atrium sessions."* (`my_sessions`) lists the assistants you are connected with: name, start and last use. The names are chosen by the assistants themselves when they connect, so read them as information, not as proof of who is behind them.
- **Lost a device or suspect misuse?** *"End all my Atrium sessions."* (`revoke_my_sessions`) logs every connection out. Each assistant must log in again. Ask for one client only by naming it.
- **What the assistant may store.** Only on your explicit request. Do not ask it to file passwords, keys or personal customer data.
- **Confirmations.** Actions that change or remove things (moving, archiving, deleting, revoking logins) need your confirmation. Read what the assistant says before you agree.
- **Limits for everybody.** Atrium limits how fast one user can read (120 requests a minute) and write (20 a minute). You will only notice it if an assistant loops; wait a minute.

## Daily workflows

**Before answering a question about the organisation.** Ask your assistant: *"Check Atrium first: what is our policy on …?"* Verify the entry's date and space in the answer.

**Staying up to date.** Once a week, or after time off, ask *"What is new in Atrium since …?"* and open what concerns you. There are no notifications yet; this is the way to catch up.

**Reporting a wrong or outdated entry.**
1. Ask *"Who are the curators of the space this entry is in?"* (`space_contacts`).
2. Tell them the entry's title or ID and what is wrong. Curators can correct or archive it.
3. Do not edit entries you do not own: authors only change their own, curators change others.

**Capturing something worth keeping.**
1. Write it up in your own words, or ask the assistant to summarise the chat.
2. *"Save this in my Atrium space."* Check the title and tags the assistant proposes.
3. If it deserves to be shared, ask an author or curator of the right space to file it there, or move it yourself if you are an author there (see the [author guide](02-author.md)).

**Switching assistants mid-task.** Save a handoff in the first assistant, load it in the second.

**Using a team procedure.** Run its skill (`/name`) instead of re-describing the process each time. If the skill is wrong, report it to its curators, who can publish a fixed version.

**Leaving the team.** Before your last day, move work that belongs to the team out of your personal space (ask an author of the target space, or ask your administrator how handover works).

## Troubleshooting

| Problem | What to try |
|---|---|
| The assistant does not know about Atrium | Mention "Atrium" in your request, or start a new chat. Check that the connector is enabled for the chat |
| "Not found" for an entry or space | You may lack access, or it was archived. Ask a curator or admin. For spaces and entries you cannot see, Atrium reports a missing right and a missing item the same way on purpose |
| Login fails | Your account may not be allowed, or it was locked. Contact your administrator and give the time of the attempt |
| "Too many requests" | Wait a minute. Atrium limits reads and writes per user |
| Search finds nothing | Use other words, search without filters, or check your spaces with `list_spaces` |
| A skill is not offered | It may be unreleased, blocked or archived, or in a space where you are not a member |
| You need to report an error | Ask *"Which Atrium version is this?"* (`server_info`) and include it, plus the time and what you asked |

## FAQ

**Do I need to learn commands?** No. Describe what you want. The tool names in this guide are for reference and for reports.

**Can my colleagues read my personal notes?** No. Only you can. Instance admins cannot either.

**Can the assistant change or delete things without asking?** No. It acts only on your explicit request, and destructive actions need your confirmation.

**Who sees what I save in a team space?** All readers of that space, once the entry is approved. In spaces with approval, curators check it first.

**Why does the assistant say an entry may be outdated?** Entries have a date and a version. It is told to point out age and contradictions.

**Why can I not find an entry a colleague mentions?** It may be in a space you cannot access, still a draft, or archived. Ask the author or a curator.

**Can a colleague put something into my personal space?** Only where your organisation allows sending, and only as a draft that you alone see and decide on. It does not reach your assistant before you approve it, and you can return it without giving the sender a reason they can read.

**What is the difference between a skill and an entry?** An entry is information; a skill is an instruction the assistant follows when you ask. Skills are reviewed and versioned.

**Can I use Atrium from several assistants at once?** Yes, each assistant connects separately and appears in your sessions.

**How do I stop an assistant from using Atrium?** Remove the connector in the assistant, and end its session with `revoke_my_sessions`.

**How much does the assistant read at once?** A search returns the best matching sections, five by default and at most twenty, not whole documents. A full entry is loaded only when the assistant opens it by its ID.

**Where is my data stored?** On the Atrium server of your organisation. Search indexing may use an embedding service chosen by your administrator.

**How do I find out what changed while I was away?** Ask *"What is new in Atrium since 1 March?"*. It lists new and changed entries and released skills of your team spaces since that date.
