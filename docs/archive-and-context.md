# Archive and context

## Archive

An archived entry (`archive_knowledge`) disappears from the active knowledge base: search, lists and loading by ID no longer show it to unauthorized users. It is not deleted; text, search index and version history are kept.

**Who sees the archive:** exactly those who may also restore an entry, i.e. its author (from the author role up) and the curators and admins of the space (instance admins in any team space). Readers and other authors see nothing. Personal spaces: only the owner.

| Task | Tool |
|---|---|
| List the archive of a space | `list_knowledge` with `status=archived` |
| Search the archive | `search_knowledge` with `archive=true` |
| Read an archived entry | `get_knowledge` (status `archived`) |
| Restore | `unarchive_knowledge` (with `confirm=true` after asking) |

`archive=true` searches **only** the archive, never both at once. This way outdated entries do not end up unnoticed in an answer. Archive search is meant for the exceptional case, for example to find an entry to restore, and the assistant uses it only on explicit request.

**Restoring:** The entry gets the status it had before archiving. A previously approved entry by an author in a space with mandatory approval goes back into review as a draft, so the archive is not a way around approval. `archive`, `unarchive` appear in the audit log with the version.

Archived entries do not count toward the quota of a space; it is checked on restore.

## Context

Connected assistants (Claude, ChatGPT, …) do not know on their own what is in the instance. The server therefore sends them hints when they connect (the MCP instructions), consisting of three parts:

1. Base text: name, short name, "store only on explicit request".
2. **Instance admin context:** a freely worded text on what can be found here and when to search.
3. **The user's spaces:** name and description of all team spaces this user may read (at most 20). Spaces without access do not appear.

The instance admin sets the context with `admin_context`:

- `action=get` shows it, `set` with `text` replaces it completely (at most 2000 characters), `clear` removes it. `set` and `clear` need `confirm=true` after the user has seen the old and the new text. The same applies to changing a space description (`admin_update_space` with `description`).
- It is stored in the table `meta` and takes effect immediately for new connections. A chat that is already open keeps the hints it was started with.
- Changes are recorded in the audit log (`admin_context_set`, `admin_context_clear`) with old and new text, descriptions with `previous_description`. The context reaches the assistants of all users, so it should be set only on the admin's explicit instruction, never from the contents of entries.

The base text also carries the **short name** and the **organisation name** of the instance. They are read from `instance.short_name` and `instance.organization_name` (usually via `ATRIUM_SHORT_NAME` and `ATRIUM_ORGANIZATION_NAME` in `.env`) on the first start only and stored in the table `meta`. From then on the instance admin changes them with `admin_instance` (`get`; `set` with `short_name` and/or `organization_name`, after `confirm=true`), without a restart. They also appear in the tool descriptions, and new connections see a change at once. The change is audited as `admin_instance_set` with the old and new names. Give every instance its own names when an assistant is connected to more than one.

Example:

> Hier liegt das Unternehmenswissen von Example Corp: Richtlinien, Prozesse, Produkt- und Kundenwissen. Wer eine Frage dazu stellt, durchsucht zuerst diesen Speicher, bevor er antwortet.
> (This is where Example Corp's company knowledge lives: policies, processes, product and customer knowledge. Anyone asking a question about it searches this store first before answering.)

Subject-matter scoping belongs in the **description of the space** (`admin_update_space`, field `description`), for example "Support: solutions to customer inquiries". It appears automatically in the hints.

How strictly a client follows the hints is up to the vendor. A clear sentence with a trigger ("for questions about …, search first") works better than a list.
