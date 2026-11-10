# Sending to a colleague

A user can send a knowledge entry or a skill to a colleague. The copy lands in the **recipient's personal space as a draft**, and the recipient, who owns that space, is its only reviewer: they read it, then approve or return it with the ordinary review tools. Nothing reaches the recipient's assistant before that.

It is for the cases where a team space is the wrong place: a checklist for one person, a skill a colleague wants to try, something that is not yet for everybody. What belongs to the team goes to a team space (see [approval](approval.md) and [skills](skills.md)).

Sending is **off by default** and switched on in `config.yaml`:

```yaml
sharing:
  enabled: true
  max_pending_per_recipient: 20
  max_pending_per_sender: 3
```

It needs `spaces.personal.enabled`, because the copy lands in the personal space. The tools `share_knowledge` and `share_skill` are listed only where sharing is enabled; the server refuses the call otherwise as well.

## What happens

| Step | Who | Tool |
|---|---|---|
| Send an approved entry, or the newest released version of a skill, by e-mail address (with confirmation) | sender | `share_knowledge`, `share_skill` |
| See how many items wait | recipient | `whats_new` (`sent_to_you`), `list_knowledge status=draft`, `list_skills status=draft` |
| Read it, approve or return it | recipient | `review_knowledge`; `review_skill` and `publish_skill` for a skill |

- **The sender is the author** of the copy and is shown as such to the recipient (`author`). The sender can neither read nor change the copy afterwards, and is not told what the recipient decided.
- **An entry** arrives as a draft; approving it makes it an ordinary entry of the personal space, visible to the recipient alone. A binding rule (type `rule`) arrives as plain documentation, because rules exist only in team spaces.
- **A skill** arrives as version 1, a draft with the same files and digest. No release travels along, as with `move_skill`. The recipient reads every file, approves it with a risk class (`review_skill`, with the digest of what was read) and releases it (`publish_skill`); only then does their assistant offer it. Skills of risk class C can be released only by an instance admin, as everywhere.
- **Rejecting** needs a note, as always; the sender never sees it. A returned item stays in the personal space until the recipient archives or deletes it, and it keeps its place against the limits below until then. A skill of the same name has to be deleted before another one of that name can be sent.

## Who may send what

| Source | Who may send it |
|---|---|
| Entry in the sender's own personal space | the sender |
| Entry in a team space | its author (author role in the space), curators and admins of the space, instance admins |
| Skill in the sender's own personal space | the sender |
| Skill in a team space | curators and admins of the space, instance admins (the same as for `move_skill`) |

Only **approved** entries and **released** skills can be sent; the sender needs the right to change the entry, which is why a reader cannot hand team content to people outside the space. Text that is not stored yet is stored in the personal space first and sent from there.

## What protects the recipient

- **Limits.** Only a few received items may wait unanswered in a personal space: `max_pending_per_recipient` from everybody together (default 20) and `max_pending_per_sender` from one sender (default 3). Drafts and returned items count; archiving or deleting frees the place. The recipient's quota applies too.
- **One answer for everything on the recipient's side.** An unknown or disabled account, a recipient without a personal space, a name that is already taken, a full space and a reached limit all produce the same refusal ("could not be delivered"). The sender learns nothing about who exists or what they hold. For the same reason the recipient's entries are not checked for duplicates.
- **No self-service around the review.** The copy is a draft in a space whose only member is the recipient. Nobody else, instance admins included, can read it.
- **Rate limit.** Sending is a write; the write limit per user applies.
- **Audit.** Every sending is recorded as `share_send` of the sender, with the recipient (id), the kind (`knowledge` or `skill`) and a digest, never a title, a name or content. The recipient's decisions are ordinary events of their personal space and keep no details.

Entries are untrusted data and a skill is an instruction. The tools say so: the review tools present a received item as coming from its sender, and a received skill as an instruction written by a person, not approved by the organisation.

## Offboarding

Received items are part of the leaver's personal space and follow it (see [offboarding](offboarding.md)). A sender who leaves does not change anything for the recipient; the copy stays with the sender as author.

## Not part of this version

No notice to the sender, no recall of a sent item, no list of sent items and no switch for a single user to refuse items. Whether and how these come is in the [roadmap](../ROADMAP.md).
