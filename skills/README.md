# Skills shipped with Atrium

Two skills for the daily care of a knowledge store. The assistant does the reading and writing; Atrium keeps rights, review, versions and the audit log. The idea follows the "LLM wiki" pattern: understand a source once, when it is filed, and check the result regularly, instead of searching raw material for every question.

| Skill | What it does | Who uses it |
|---|---|---|
| [knowledge-ingest](knowledge-ingest/SKILL.md) | Turns a source the user provides (document, notes, a web page the assistant read) into entries: compares with what Atrium knows, proposes new entries and updates, saves what the user confirms | authors |
| [knowledge-lint](knowledge-lint/SKILL.md) | Checks a space or topic for outdated entries, duplicates, contradictions, broken references, sensitive content and waiting drafts, and fixes what the user confirms | curators and space admins; readers get a report |

Both use only the existing knowledge tools. They never delete, never approve, and change nothing without the user's confirmation.

## Atrium does not install them by itself

A skill steers assistants. Each organisation decides whether to offer one, and reviews it like its own skills. Nothing here is installed, published or announced unless somebody stores it in a space and a curator releases it.

## Installing a skill

1. Give an assistant that is connected to Atrium the files of one skill folder (Claude Code can read the folder; in a chat client attach the files) and say: *"Store this skill in Atrium in the space General."* It calls `store_skill` with the folder name as the name and every file with its path relative to the folder (`SKILL.md`, `references/...`).
2. A curator of that space other than the one who stored it (or an instance admin) reviews it with `review_skill`. Both skills use tools and write entries after confirmation: **risk class B**.
3. A curator releases it with `publish_skill`.

Store it in a space that the intended users can read, for example the general team space; released skills are announced to every reader of their space. Users then ask for it by name, for example `/knowledge-ingest`, or with *"Use the knowledge-lint skill on the space Support."*

## Updates and adapting

The skills belong to the release they ship with: a new release may change them together with the tools they use. To update, store the files of the new release under the same name; that is a new version and goes through the review again. Compare the digest shown by `get_skill` with your installed version to see whether anything changed.

You may adapt a copy (language, the entry template, the age threshold). Keep the Rules sections: they carry the safety of the skill.

## Checked with every build

Two tests keep these files honest: `internal/skills` checks that every folder is a valid package under the rules of `store_skill`, and `internal/mcp` checks that every tool and parameter a skill names exists in this build. A renamed tool fails the build, not a customer's chat.

## Limits

- `list_knowledge` returns at most the 100 most recently changed entries of a space. In larger spaces the lint works tag by tag or topic by topic and says what it did not check.
- There is no server-side help yet for finding similar pairs in a space or related entries for a long source; the assistant uses `search_knowledge` for both. See the [roadmap](../ROADMAP.md).
- How faithfully an assistant follows a skill depends on the client. Test the clients you rely on.
