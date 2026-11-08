# Atrium for space admins

A **space admin** looks after one team space: who belongs to it and with which role, and the last line of decisions that curators cannot take (lifting a skill block, deleting for good). This guide assumes you know the guides for [everyday use](01-user.md), [authors](02-author.md) and [curators](03-curator.md): a space admin has every right of a curator in the space.

Do not confuse the two admin roles. A **space admin** manages **one** space. An **instance admin** runs the whole Atrium instance, counts as admin in every team space and has the `admin_*` tools ([guide 5](05-instance-admin.md)). Some settings of your space can only be changed by an instance admin; the table below says which.

## Contents

- [What space admins can and cannot do](#what-space-admins-can-and-cannot-do)
- [Members, roles and groups](#members-roles-and-groups)
- [Choosing roles](#choosing-roles)
- [Lifting blocks and deleting](#lifting-blocks-and-deleting)
- [Settings only instance admins change](#settings-only-instance-admins-change)
- [Daily and periodic workflows](#daily-and-periodic-workflows)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)

## What space admins can and cannot do

| | Space admin | Instance admin |
|---|---|---|
| Everything curators can do in the space | yes | yes |
| See who belongs to the space (`space_members`) | yes | yes |
| Add or change members, users and groups (`space_add_member`) | yes | yes |
| Remove members (`space_remove_member`) | yes | yes |
| Lift a skill block (`unblock_skill`) | yes | yes |
| Delete entries for good (`delete_knowledge`) | yes | yes |
| Delete skills (`delete_skill`) | no | yes |
| Publish skills of risk class C | no | yes |
| Create a space, change its name, description, approval requirement or default role | no | yes (`admin_create_space`, `admin_update_space`) |
| Archive or restore the whole space | no | yes (`admin_archive_space`, `admin_restore_space`) |
| Create groups, manage logins, read the audit log | no | yes |
| See and manage spaces you do not administer | no | all team spaces |
| Read personal spaces | no | no |

A space admin only sees their own space(s) in the member tools. Spaces they do not administer are reported as "not found".

`space_members` also shows the **limits and the current usage** of the space (entries, bytes, skills, skill bytes and versions per skill, with the source of each limit). Limits are set by instance admins; if your space is about to run into one, ask for it to be raised.

## Members, roles and groups

### Who is in a space

*"Who is in the Support space?"* (`space_members`) lists every member (people and groups) with the role, and shows two settings: whether the space **requires approval**, and the **default role for everybody logged in** (if any). Someone can have access without being listed when the space is open to everybody logged in as a reader or author.

### Adding a member or changing a role

*"Add maria@example.com as author in the Support space."* (`space_add_member`)

- The person must have **logged in at least once** before you can add them. If Atrium says it does not know the user, ask them to connect an assistant first.
- The same command **changes a role**: add the person again with the new role.
- You can add a **group** instead of a person: *"Add the group 'support-team' as curator in Support."* A person gets the best of their own role, their groups' roles and the default role. Local groups are managed by instance admins; groups from your identity provider are not imported yet.
- A change takes effect immediately for new requests (the role cache is cleared on every membership change).
- Every change is recorded in the audit log with the person, the role and who made it.

### Removing a member

*"Remove maria@example.com from the Support space."* (`space_remove_member`, after your confirmation.) The person's entries stay, with their name as author. They lose access if they have no other role (for example through a group or a default role). Atrium does not prevent you from removing the last admin; an instance admin can always step in, but avoid it.

## Choosing roles

Give people the lowest role that lets them do their job:

| Role | Give it to | Notes |
|---|---|---|
| **Reader** | People who consume knowledge | Often covered by the default role |
| **Author** | People who document and maintain | Their entries are drafts when the space requires approval |
| **Curator** | Subject experts who review | Needed at least **twice**: curators cannot approve their own drafts or skill versions |
| **Admin** | The one or two people who manage membership | They also review as curators |

Rules of thumb:

- **Two curators per space** with approval, so that review continues when one is away.
- **Do not make everybody a curator.** Review is a control; fewer, careful reviewers beat many hurried ones.
- **Prefer groups** over long lists of people, if your identity provider or the instance admins provide matching groups. Access then follows the group.
- **Authors review nothing, curators are not admins.** An admin should not routinely approve their own work; Atrium lets admins do it to avoid deadlocks, and marks it in the audit log.
- Skills are a higher-trust content type: be more careful with who may publish them (curators) and who may unblock (you).

## Lifting blocks and deleting

### Lifting a skill block

A curator blocks a skill version when it turns out to be harmful or wrong. Only **you or an instance admin** can lift the block (`unblock_skill`):

1. Find out why it was blocked: the reason is recorded. *"Show the blocked versions of support-reply."* (`list_skills` with `all_versions`).
2. Check that the problem is solved. Usually the right answer is *not* to unblock the old version but to have the author file a corrected version for review.
3. If you do unblock (*"Unblock version 2 of support-reply, reason: the wrong step was fixed in a review on …"*, after confirmation), the version becomes **approved**, not published. A curator releases it again (class C: an instance admin). The files are checked against their digest at that point.

A reason is required and goes to the audit log.

### Deleting entries for good

`delete_knowledge` removes an entry with its version history. It cannot be undone and is meant for cases like personal data that must not be kept, or content filed in the wrong place by mistake. In all other cases **archive** instead (`archive_knowledge`): nothing is lost and it can be restored. Deletion needs your confirmation. Curators cannot do it. It also works on archived entries, so archiving an entry first (to stop it being served at once) does not stand in the way of deleting it for good.

Deleting skills for good is for instance admins, because skills are instructions that assistants followed. Ask an instance admin if one must go; block or archive in the meantime.

## Settings only instance admins change

Ask an instance admin when you need to:

- create a new space, rename it, change its description (the description appears in assistants' instructions, so keep it short and clear),
- raise or lower the **storage limits** of the space (`admin_quotas`, effective at once),
- switch **approval** on or off for the space,
- set the **default role** for everybody logged in (reader or author) or remove it,
- archive or restore the space,
- create or change local groups (groups from your identity provider are not imported yet),
- see who looked at or changed something (audit log),
- publish a class C skill or delete a skill,
- unlock someone who cannot log in.

## Daily and periodic workflows

**Onboarding a new team member.**
1. They log in once with their assistant.
2. You add them (person or group) with the right role, usually reader or author.
3. Send them the [everyday-use guide](01-user.md) and, for authors, the [author guide](02-author.md).
4. Check with *"Who is in the Support space?"*.

**Changing someone's role.** Add them again with the new role. For promotion to curator, check that they know the review rules ([curator guide](03-curator.md)).

**When someone leaves the team.**
1. Hand over what they answer for: *"Which entries in Support is maria@example.com responsible for?"* (`list_knowledge` with `owner`). A curator or the author then changes the responsible person entry by entry (`update_knowledge` with `owner`); the Pro edition can hand them all on in one step ([Atrium Pro](../pro/pro-edition.md#handing-on-responsibilities-space-admin-instance-admin)).
2. Remove them from the space.
3. If they leave the organisation, an instance admin locks the account and decides about their personal space ([Offboarding](../offboarding.md)). Ask them to move team knowledge out of their personal space first. Locked people stay members (so that a reactivation restores everything) but have no rights; `space_members` and `space_contacts` mark them `former`. Entries that name them as responsible keep the name, marked `former`, until you reassign them.
4. Check that the space still has two **active** curators and an admin.

**Quarterly access review.**
1. *"Who is in each of my spaces?"* Compare with the current team.
2. Remove people who left or no longer need access, lower roles that are higher than needed.
3. Check that curators are active: do drafts and skill drafts wait too long? If so, add or replace curators.
4. Review group memberships with your instance admin.

**Settling a dispute about content.** If an author and a curator disagree, the space admin decides: have the curator return the draft with a clear note, or approve it yourself (admins may approve; the audit log marks self-approval of your own drafts).

**Handling an incident** (a skill or entry caused harm).
1. A curator blocks or archives the content; you confirm that it is done.
2. Tell the instance admins; they can read the audit log to see who loaded the skill and when.
3. Decide with the author and curators how to fix it, and who reviews the new version.
4. Only then unblock or accept the new version.

**Deleting data on request (data protection).** If someone asks for personal data to be removed from the space, find the entry, check it, and delete it for good (`delete_knowledge`). Record the request in your own process; the audit log keeps the action and the title of the entry, not its content, so keep personal data out of titles.

## Troubleshooting

| Problem | What to try |
|---|---|
| "User unknown, they must log in once first" | Ask them to connect an assistant and log in, then add them again |
| The person still has access after removal | They have it through a group or the default role. Check `space_members` and the groups |
| "Not found" for a space you manage | Check the spelling or slug. You see only spaces you administer |
| You cannot change the approval setting | Only an instance admin can (`admin_update_space`) |
| A curator cannot approve their own draft | By design. Another curator, or you as admin, approves |
| Unblocking did not make the skill available | The version is *approved*, not published. A curator must publish it again |
| Role changes seem not to apply | They apply to the next request. Check `space_members`, and look for roles the person has through a group or the default role |

## FAQ

**Can I see what is in someone's personal space?** No. Nobody can, not even instance admins.

**Can I turn on approval for my space?** No, ask an instance admin.

**What is the difference between archive and delete?** Archive hides and keeps (restorable). Delete removes for good with the version history.

**Can I add a person who has never logged in?** No. They must log in once.

**What if I am the only admin and I leave?** Hand the role to someone else first. If you forget, an instance admin can still manage the space.

**Do role changes apply to open chats?** Yes. The assistant's next call uses the new role.

**Can I lock a user out of Atrium?** Only for your space (remove them). Locking an account entirely is for instance admins.

**Who can see who is in my space?** Space admins and instance admins see the member list; members see the curators and admins through `space_contacts`.

**Can a group be admin?** Yes, but prefer named people for admin and curator so that responsibility is clear.
