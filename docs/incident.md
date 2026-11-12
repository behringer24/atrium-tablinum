# Incident runbook

What an instance admin does when something goes wrong in a running Atrium. Each step names the tool or command; the audit log
(`admin_audit_log`) is the record of what happened. Do the **containment** steps first, the investigation afterwards.

Vulnerabilities in Atrium itself are reported as described in [SECURITY.md](../SECURITY.md).

## Containment tools

| Tool | Effect |
|---|---|
| `admin_revoke_sessions` | Ends all logins of one user; the account stays active. |
| `admin_offboard_user` | Locks one account, revokes its tokens and deletes its handoffs. `admin_reactivate_user` lifts it. |
| `admin_revoke_all_sessions` | Ends the logins of **all** users of the instance at once, yours included. Accounts stay active, every assistant logs in again. Needs `confirm=true`. |
| `admin_allowlist` | Closes the login to a domain, address or tenant; existing sessions stay until revoked. |
| Stopping the server | `docker compose stop server` takes the instance offline; data and tokens stay. |

`admin_revoke_all_sessions` covers OAuth logins. A static token of a development setup (`dev_mode`) is not an OAuth login and is
not affected; `dev_mode` does not belong on a server in the first place.

## Playbook

| Situation | Steps |
|---|---|
| One account may be compromised | `admin_revoke_sessions` for the user, then `admin_offboard_user` if confirmed. Review `admin_audit_log user=...`. Reset access at the identity provider. |
| Tokens may have leaked (a log, a backup, a laptop, a proxy), but the scope is unclear | `admin_revoke_all_sessions`. Tell users they have to log in again. Find and remove the source of the leak first, otherwise the new tokens leak too. |
| The server or its host may be breached | Stop the server. Keep the database and logs as evidence (copy them). Rotate every secret the server held (database passwords, identity provider client secrets, embedding and cloud keys), restore or rebuild on a clean host, start, then `admin_revoke_all_sessions`. |
| Unusual activity in the audit log | `admin_audit_log` filtered by user, client, action and time. Revoke what you cannot explain; ask the user before assuming misuse. |
| A harmful or wrong skill | Block the version (`block_skill`), check who loaded it (`admin_audit_log skill=...`), tell the users, have a fixed version reviewed. |
| Sensitive data or credentials in an entry | Archive it at once to stop it being served, rotate credentials first if there were any, then delete it for good. Reads of entries are not logged: the audit log shows who changed it and which searches ran, not who read it. |

## After the incident

1. Write down the timeline from the audit log: what, who, when, through which client.
2. Check the data protection duties (reporting deadlines apply to personal data); involve the data protection officer.
3. Close the cause: patch release, configuration, access rights.
4. Lift locks (`admin_reactivate_user`) only after the cause is closed.
5. If the cause is a flaw in Atrium, report it ([SECURITY.md](../SECURITY.md)).

## Evidence

The audit log is append-only for the server when the separate database accounts are in use
([deployment.md](deployment.md), "Separate database accounts"); without them anyone holding the server's database connection
could change it. Back up the database before you restore or rebuild anything.
