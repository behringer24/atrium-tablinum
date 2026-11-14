# Getting started with Atrium

Your first steps with Atrium Tablinum (short: **Atrium**): connect your assistant, ask your first question, save your first note and find it again in a new chat. It takes about five minutes. This guide is for new users; it shows the way and points to the [everyday use guide](01-user.md) for the details.

## Contents

- [What Atrium does for you](#what-atrium-does-for-you)
- [1. Connect your assistant](#1-connect-your-assistant)
- [2. Find what your organisation knows](#2-find-what-your-organisation-knows)
- [3. Your personal space](#3-your-personal-space)
- [4. Share knowledge with colleagues](#4-share-knowledge-with-colleagues)
- [5. Know your spaces and roles](#5-know-your-spaces-and-roles)
- [6. Keep knowledge up to date](#6-keep-knowledge-up-to-date)
- [7. Stay informed](#7-stay-informed)
- [8. Skills: shared working instructions](#8-skills-shared-working-instructions)
- [9. Continue in another assistant](#9-continue-in-another-assistant)
- [10. Privacy and safety](#10-privacy-and-safety)
- [Your first five minutes](#your-first-five-minutes)
- [FAQ](#faq)

## What Atrium does for you

Imagine your assistant knows more than the world: it also knows your organisation, with its processes, decisions, experience and the knowledge of your colleagues. And it can remember what you explicitly entrust to it, across new chats and across different assistants. That is what Atrium provides.

- **As a reader** you ask your usual assistant, in your own words, how a process works, which rule applies or what was decided. Instead of searching documents or asking colleagues, you get an answer based on what your organisation has filed, with the source named.
- **As an author** you tell your assistant to file a finding: a solution, a decision, a better way of working. It writes the entry and files it where you say. Colleagues, and you yourself in later chats, can then use it.

You do not learn another application. Atrium has no screen of its own: you keep working in your assistant, and the assistant uses Atrium on your behalf. Atrium never listens to your conversations and saves nothing unless you ask for it.

## 1. Connect your assistant

Two things are different: the Atrium **connector** has to be available in your assistant, and **you** have to log in to Atrium. Often your organisation has done the first step already.

### Step 1: make the connector available (perhaps already done)

Open the list of apps, connectors or MCP servers in your assistant and look for Atrium.

- **If Atrium is listed:** you do not need to know the address. Continue with step 2.
- **If it is not listed:** ask your administrator to enable it, or add it yourself where your organisation allows that. In the settings for apps, connectors or MCP servers, add a custom connector (Claude, ChatGPT) or MCP server (Claude Code and others) and enter the address you got from your organisation. It ends in `/mcp`, for example `https://atrium.example.com/mcp`.

The names and options differ between assistants, and not every user may create connectors.

### Step 2: log in

1. Select **Atrium** in your assistant and choose **Connect** (or the equivalent option). If you are already connected, this step does not appear.
2. A login page opens. Log in with your organisation account, for example Google or Microsoft, and confirm the permissions if you are asked.
3. After the login your assistant can use Atrium with your own rights.

There is no separate Atrium password. If the login fails, ask your administrator whether your account is allowed to use Atrium. Tools may be loaded on demand, so in a fresh chat the assistant sometimes needs a moment before it knows what Atrium contains. Each assistant you use is connected and logged in once.

**Your first test:**

> *Which Atrium spaces can I use, and what is my role in each?*

The assistant lists the spaces you may use.

## 2. Find what your organisation knows

The most important function is also the simplest: ask in plain language.

> *What is our holiday policy?*
>
> *Which decisions were made in project X so far?*
>
> *What experience do we have with system Y?*
>
> *Which binding rules apply to business travel?*

The assistant searches the spaces you may read. You can ask for a search explicitly, or narrow it:

> *Search Atrium for our approval process.*
>
> *What does the IT space say about the VPN?*

If you mention "Atrium", the assistant knows to use this store. Without the name it decides itself whether a question might concern company knowledge, so name Atrium whenever you want to be sure.

### Where does the answer come from?

Ask for the sources whenever it matters:

> *Show me the Atrium entries your answer is based on.*

The assistant names title, space, type and date of each entry. Documented knowledge can be out of date or contradict itself, so check important decisions.

### Not everything is a rule

Atrium distinguishes four types of entries, and your assistant takes them into account:

| Type | Meaning |
|---|---|
| Rule | A binding requirement of the organisation |
| Documentation | Describes processes, facts or decisions |
| Recommendation | A suggested way of working |
| Experience | Findings from practical work |

## 3. Your personal space

Every user has a personal space. Keep there what you want to reuse across chats without sharing it, for example work notes and checklists, insights from your day, project thoughts and drafts. You can use it without any role.

Say you built a detailed checklist for a recurring task. Instead of explaining it in every new chat:

> *Save this checklist in my Atrium space.*

Later, in a new conversation:

> *Show me my checklist for this task.*

You can ask the assistant to extend it as well:

> *Add a final quality check to my checklist.*

**Your personal space is private.** Nobody else can read it through Atrium, instance admins included. Direct access to the database or to backups is a different matter and is not covered by that. Your space is for work-related notes and has a limit (by default 500 entries and 10 MB; archived entries do not count). If you leave the organisation, you are asked to move what belongs to the team before your last day, and the rest is deleted after a deadline. See [Offboarding](../offboarding.md).

Without a space named, new entries go to your default space, normally your personal space. Say *"Make the Support space my default"* to change that.

## 4. Share knowledge with colleagues

Atrium becomes most valuable when knowledge is shared. For that there are **team spaces**, for example for a department, a team or a topic. To file something there you need the author role in that space.

Say you worked out a new way to handle a customer request:

> *Turn the process we just discussed into clear documentation and file it in the Support space.*

The assistant structures it, gives it a clear title and files it. When a colleague asks about that process, their assistant can use your entry, provided the colleague may read the space.

### What happens after you file it?

That depends on your role and on the space. In some spaces a new entry is visible to all readers at once. In others there is an approval: your entry is stored as a **draft**, and a curator reviews it before the team sees it. Your assistant tells you which case applies.

### If something similar already exists

Atrium checks whether a very similar entry is already in the space. If it is, nothing is saved yet: the assistant shows you the existing entry and asks whether to update it or to file a new one anyway. That keeps the space free of duplicates and contradictions.

How to write entries that others can use is described in the [author guide](02-author.md).

## 5. Know your spaces and roles

Knowledge is organised in spaces. You see only the spaces and contents you may use, and your role can differ from space to space.

| Role | What you can do |
|---|---|
| Reader | Read and search knowledge, use released skills |
| Author | Also file your own entries and maintain them within the rules of the space |
| Curator | Also review and approve entries and skills |
| Space admin | Also manage members and roles of the space |

You can be an author in the IT space and a reader in the HR space at the same time. Ask:

> *Which spaces do I have in Atrium, and what can I do there?*

If you need more rights, ask the people responsible:

> *Who is responsible for the IT space in Atrium?*

## 6. Keep knowledge up to date

Knowledge changes: processes are adapted, decisions are revised, new findings arrive. You can maintain entries later:

> *Add the new review step to my entry about the approval process.*
>
> *Show me all Atrium entries about invoicing.*
>
> *Which entries about project X might be outdated?*
>
> *Archive my old entry about project X.*

What you may change depends on your role; in spaces with approval some changes are reserved for curators. Atrium keeps a version history. Archiving takes an entry out of the normal search without deleting it, and an author or curator can restore it.

## 7. Stay informed

You do not need to search all spaces to see what is new. Ask:

> *What is new in Atrium?*

You get the new and changed entries and the newly released skills in your team spaces, by default of the last seven days. You can name a period or a space:

> *What changed in the IT space since last Monday?*

That helps after a holiday, after a longer absence or before a team meeting. Your personal space is not part of this overview.

## 8. Skills: shared working instructions

Besides knowledge, Atrium can provide **skills**. A skill is a prepared, approved working instruction that tells your assistant how to carry out a certain task, for example a standard template for project reports, a quality check or a procedure for code reviews. Which skills exist depends on your organisation.

> *Which skills are available to me in Atrium?*

When you have found a suitable skill, ask your assistant to use it. In assistants that support commands, you can start it by name, for example `/name`. Skills are versioned and reviewed before they are released, and they give your assistant no additional rights. See [Skills](01-user.md#skills) in the everyday use guide.

## 9. Continue in another assistant

Perhaps you research in one assistant and want to continue in another. A **handoff** carries the state over. In the first assistant:

> *Store the current state as an Atrium handoff for another assistant.*

Atrium keeps a summary with the goal, the results so far, open questions and next steps. In the other connected assistant:

> *Load my last Atrium handoff and continue with it.*

You do not have to explain the state again. Handoffs are meant for short-term switches: they are kept in your personal space, nobody else sees them, and they are deleted automatically after 14 days by default. For information that stays valuable, file a regular entry instead.

## 10. Privacy and safety

You stay in control of what is stored for good.

1. **Nothing is saved on its own.** The assistant files something in Atrium only when you ask.
2. **Access follows your rights.** You can read or change only what you are allowed to.
3. **Your personal space is private.** Other users, administrators included, cannot read it through Atrium.
4. **Critical changes need your confirmation.** Archiving, moving and deleting are done only after you agree.
5. **No secrets.** Passwords, API keys and other credentials do not belong in Atrium, nor does personal customer data. Atrium rejects text that looks like a password or key, but it does not recognise everything.
6. **You see your connections.** Ask *"Show my active Atrium sessions."* and, if needed, *"End all my Atrium sessions."* The assistants concerned then have to log in again.

Logins also end on their own: every login lasts 90 days at the latest. More in [Your logins and your safety](01-user.md#your-logins-and-your-safety).

## Your first five minutes

Do you want to try Atrium now? These five steps cover the most important functions.

- [ ] **1. Connect.** Choose the Atrium connector, select Connect and log in. If it is not available yet, ask your administrator.
- [ ] **2. Discover.** Ask *"Which Atrium spaces can I use?"*
- [ ] **3. Search.** Ask a question about an internal topic, for example a rule or a process.
- [ ] **4. Save.** File a simple note in your personal space.
- [ ] **5. Find it again.** Open a new chat and ask the assistant to retrieve the note you just saved.

In step 5 your assistant uses knowledge that you did not explain again in the new chat. That is the point of Atrium.

One habit makes the difference: **whenever you have to explain something for the second time, ask yourself whether it belongs in Atrium.**

Where to go next: the [everyday use guide](01-user.md) for all functions, the [author guide](02-author.md) if you file knowledge in team spaces, and [Why Atrium](00-why-atrium.md) if you want to show colleagues what it brings.

## FAQ

**Do I have to mention "Atrium" in every question?**
No. Your assistant can recognise from the descriptions Atrium provides that it may hold useful knowledge. Name it when you want to be sure, for example *"Search Atrium for ..."*.

**Can the assistant simply save everything from my conversations?**
No. Atrium stores something only when you explicitly ask for it.

**Can I use Atrium with several assistants?**
Yes. You connect each assistant once with the same account. All of them work with the same knowledge, within your rights.

**Can my colleagues read my personal notes?**
No. Only you can, through Atrium; instance admins cannot either. For work-related data the offboarding rules of your organisation apply when someone leaves.

**Why can I not find an entry?**
It may be in a space you may not read, it may still wait for approval, or it may be archived. Ask for your spaces and, if needed, for the contacts of the space.

**Can I correct wrong information?**
Yes, if your role allows it. Otherwise ask a curator of the space to correct the entry.

**Do I have to open Atrium separately?**
No. You use it in the conversation with your assistant.

**What if the login does not work?**
Ask your instance admin whether your account is allowed to use Atrium. Troubleshooting for common cases is in the [everyday use guide](01-user.md#troubleshooting).
