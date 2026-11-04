# Offboarding

Personal spaces are visible only to their owner, not even to instance admins. When someone leaves, an instance admin decides what happens to the knowledge in it. All steps run through MCP tools and appear in the audit log, there only with account and count, never with titles or contents.

## Procedure

1. **Before the last working day:** The employee moves what belongs to the team themselves with `move_knowledge`. This is the cleanest way because they decide themselves.
2. **Lock:** `admin_offboard_user` locks the account immediately, revokes all OAuth tokens and deletes the handoffs. The personal space stays private; only count, size and deletion date (`purge_after`) are returned.
3. **Transfer:** `admin_transfer_personal_space` works only for locked accounts.
   - `mode=space` converts the personal space into a team space, e.g. "Legacy Max Muster" (German: "Nachlass Max Muster") with the supervisor as `admin` and the successor as `curator`. Entries, IDs and version history are kept. The members review, move (also into their own personal space) or delete; afterwards the space is archived with `admin_archive_space`. The successor needs `curator` because authors may only change and move their own entries. Like every team space it is visible to instance admins. Skills the owner had released alone in the personal space were never reviewed: their released versions are blocked on conversion (`skill_block` in the audit log); a space admin lifts a block with `unblock_skill` and a curator releases the version again.
   - `mode=merge` moves all entries into the successor's personal space, who then owns them like their own. Their quota applies; if it is not sufficient, nothing is moved.
4. **Deadline:** Personal spaces of locked accounts are deleted together with their content after `spaces.personal.retention_days_after_offboarding` days (default 30), unless they were converted. A background job checks this hourly; `0` turns deletion off.

`admin_reactivate_user` lifts a lock again, for example after a mistake, and ends the deadline. If the space has already been converted or deleted, a new, empty one is created at the next login. The account itself is never deleted: its entries in team spaces keep their author.

## Data protection

Decide before going live and tell employees that the personal space is meant only for work-related knowledge and may be transferred when they leave. Carry out a transfer against the employee's will only when documented and preferably under the four-eyes principle; agree the procedure with the data protection officer and, where applicable, the works council.

The separation applies within the application. Anyone with direct access to the database or backups can technically read everything.
