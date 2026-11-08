# Offboarding

Personal spaces are visible only to their owner, not even to instance admins. When someone leaves, an instance admin decides what happens to the knowledge in it. All steps run through MCP tools and appear in the audit log, there only with account and count, never with titles or contents.

## Procedure

1. **Before the last working day:** The employee moves what belongs to the team themselves with `move_knowledge`. This is the cleanest way because they decide themselves.
2. **Lock:** `admin_offboard_user` locks the account immediately, revokes all OAuth tokens and deletes the handoffs. The personal space stays private; only count, size and deletion date (`purge_after`) are returned.
3. **Handing the content on:** an admin cannot read or hand on a personal space in the Community edition, so the person moves what belongs to the team before the last day (step 1). If you need time, lengthen or switch off the deadline of step 5. The Pro edition adds guided help for handing a personal space on.
4. **Responsibilities:** entries in team spaces that name the person as responsible keep the name, marked `former`. A curator or the author changes the responsible person entry by entry with `update_knowledge` (`owner`). The Pro edition can hand all of them on in one step.
5. **Deadline:** Personal spaces of locked accounts are deleted together with their content after `spaces.personal.retention_days_after_offboarding` days (default 30), unless the space was handed on. A background job checks this hourly; `0` turns deletion off.

`admin_reactivate_user` lifts a lock again, for example after a mistake, and ends the deadline. If the space has already been deleted, a new, empty one is created at the next login. The account itself is never deleted: its entries in team spaces keep their author, versions keep who changed them, and space memberships stay (without any rights) so that a reactivation restores them. `space_members` and `space_contacts` mark a locked member `former`.

## Data protection

Decide before going live and tell employees that the personal space is meant only for work-related knowledge and is deleted after a deadline when they leave. Agree the procedure with the data protection officer and, where applicable, the works council.

The separation applies within the application. Anyone with direct access to the database or backups can technically read everything.
