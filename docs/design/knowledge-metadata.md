# Knowledge types, responsible people and review dates

**Status:** built (see the [changelog](../../CHANGELOG.md)). This note records the decisions and their reasons.

"Approved" says that a curator let an entry through. It does not say whether the entry is binding, who answers for it, or whether it is still current. An assistant that answers a policy question needs all three. Three optional fields on a knowledge entry carry them; none is derived from anything else.

## Knowledge type

| Type | Meaning | Who sets it |
|---|---|---|
| `rule` | Binding: a policy, a regulation, a decision that applies | curators and admins of the space only |
| `documentation` | Describes how something is (default) | whoever may change the entry |
| `recommendation` | Good practice, advised but not binding | whoever may change the entry |
| `experience` | A report of what happened or worked once | whoever may change the entry |

- **A rule is the curators' business.** Only curators and admins of a team space set or remove the type `rule`, and only they change an entry of type `rule` (title, content, tags, type, responsible person, review date). Otherwise an author could quietly rewrite what the organisation declared binding, in spaces without approval as well. In a personal space the owner decides alone, as for everything there.
- **The type is part of the version.** `document_versions` records it, so the history shows when an entry became binding.
- **The type is separate from the status.** A draft can be a proposed rule; it is binding only once it is approved.
- **Assistants are told what it means.** Search hits, entries, lists and `whats_new` carry the type, and the tool descriptions say: name the type when answering; a rule is binding, recommendation and experience are not. `search_knowledge` and `list_knowledge` filter by type.

## Responsible person

- **Explicit, never derived.** An entry has a responsible person only when somebody names one. The author is not taken as a stand-in: authors write for others, curators edit, people change roles.
- **A person who can read the entry.** The responsible person must have logged in, must not be locked, and must be able to read the space. Team spaces only: in a personal space the owner is responsible anyway.
- **Who names them:** whoever may change the entry (the author for their own entry, curators for every entry; for a rule, curators only).
- **Former people stay named.** When a responsible person is locked (offboarding), the entries keep the name, marked `former`. The history of who answered for what is worth keeping, and the mark tells readers to ask the curators instead.
- **Reassigning on a change of staff.** `reassign_owner` moves every entry of one person to another in one step, with confirmation: a space admin for the spaces they administer (one or all of them), an instance admin for all team spaces. The new person must be active and able to read each space; entries in spaces they cannot read are left alone and counted, so that nobody becomes responsible for what they cannot see. `admin_offboard_user` reports how many entries name the leaver, and `list_knowledge owner=...` lists them per space. Reassigning is audited (`owner_reassign`) and creates no new version: it changes who answers, not what the entry says.

## Review date

- **A date, set on purpose.** `review_due` is the day by which the entry should be checked. Nothing sets it automatically, and nothing hides or deletes an overdue entry: an overdue entry is still the best that is known, and only a person can judge it.
- **Visible where it matters.** Entries carry `review_due` and `review_overdue`; `list_knowledge review=due` lists the overdue entries of a space, and `whats_new` tells curators how many are overdue in their spaces. The skill `knowledge-lint` can use the date instead of guessing from the last change.

## Former curators and contacts

Locking an account (offboarding) removes all rights at once but deletes nothing: the account stays, entries keep their author, versions keep who changed them, and memberships stay so that a reactivation restores them. Before this change `space_contacts` still named a locked curator like an active one. Now `space_contacts` and `space_members` mark locked people as `former`, list them after the active ones, and readers are pointed to the active curators.

## Not done, on purpose

- No groups or roles as responsible "person". A group answers for nothing; a named person does. A team mailbox can be a user if the identity provider allows it.
- No notification when a review falls due: there is no notification system yet (see the roadmap). `whats_new` and the lint skill make it visible.
- No validity period (valid from / until). A rule that ends is archived or changed; a date field would invite entries that are silently wrong after a day.
