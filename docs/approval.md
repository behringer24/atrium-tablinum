# Drafts and approval

An entry in a team space has one of four states: `draft`, `approved`, `rejected` (returned) or `archived` (see [Archive and context](archive-and-context.md)). All members can read and search only approved entries. Drafts and returned entries are visible only to their author and the curators of the space. Personal spaces have no drafts of their own; the only drafts in one are items that another user sent to its owner, who reviews them (see [sending to a colleague](sharing.md)).

## When an entry becomes a draft

- **Mandatory approval:** If the space has `require_approval`, entries by authors are always drafts. Entries by curators and admins are approved immediately.
- **On explicit request:** `store_knowledge` with `draft=true` creates the entry as a draft in any team space, even without mandatory approval and even for admins. This is meant for the case where someone writes for a subject area that is not their own and the space's curator should check the content before publication. The assistant sets `draft` only if the user explicitly asks for it.
- **Moving:** An entry that a non-curator moves into a space with mandatory approval becomes a draft there.

## Reviewing

Curators and admins of a space (instance admins in any team space) see the tool `review_knowledge`. Binding rules (`type=rule`) are set, changed and moved only by curators and admins of the space, in spaces with and without approval (see [knowledge types](design/knowledge-metadata.md)). Pending drafts are found with `list_knowledge` and `status=draft`; `whats_new` shows how many wait in each space the caller curates.

1. Load and read the draft with `get_knowledge`.
2. `review_knowledge` with `decision=approve` approves it. This makes it visible to all readers of the space and therefore needs `confirm=true` after asking the user.
3. `decision=reject` returns it to the author with a note (`note`, required). The author sees it as `review_note` on the entry.

Rules:

- **Self-approval only as admin.** A draft is a request to the space to take a look. Curators therefore cannot approve their own draft themselves, another curator does that. Admins of the space and instance admins can, so that nothing is ever left hanging when colleagues are absent or have left. In the audit log such an approval appears with `self`.
- Only drafts can be reviewed. Approved and returned entries cannot.
- A returned entry goes back into review as a draft with the author's next change (`update_knowledge`); the note is deleted in the process.
- Every review increases the entry's version and appears in the audit log with `approve` or `reject` (without the note).
- A curator who is not an admin depends on another curator for their own draft; if there is none, an admin resolves the deadlock.

## Known limits

- Authors of a space with mandatory approval cannot change approved entries themselves (`update_knowledge` reports this); a draft alongside the approved version is not yet implemented.
- There is no notification: curators find drafts only through `list_knowledge`.
