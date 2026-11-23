# Atrium Pro: connecting other systems

This guide is for the **instance admins** of an organisation that runs the **Pro** edition, and for the people who set up the tools that are connected. It covers service accounts and their tokens, and the first thing built on them: keeping the Markdown files of a Git repository, for example a code documentation, up to date in Atrium. The feature is called `service_accounts` in the licence ([Atrium Pro](pro-edition.md#the-licence)). Everything else works as described in the [instance admin guide](../user/05-instance-admin.md).

## Contents

- [What a service account is](#what-a-service-account-is)
- [Issuing an account and a token](#issuing-an-account-and-a-token)
- [Giving it rights](#giving-it-rights)
- [Managing accounts and tokens](#managing-accounts-and-tokens)
- [Rotating a token](#rotating-a-token)
- [Keeping a Git repository in Atrium](#keeping-a-git-repository-in-atrium)
- [What people see](#what-people-see)
- [Limits and security](#limits-and-security)
- [When the licence ends](#when-the-licence-ends)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)

## What a service account is

Until now everybody who works with Atrium logs in through the identity provider, and every token belongs to a person. A **service account** is an account for a tool that works on its own: a script, a CI job, an automation. It has a name, a token, and a person who answers for it. It is not a person, and Atrium treats it with that in mind:

| | Person | Service account |
|---|---|---|
| Logs in through the identity provider | yes | no, it uses a token |
| Counts against the user limit | yes | no |
| Gets the default role of an open space | yes | **no**: only roles it was given |
| Highest role in a space | admin | **curator** (an admin membership counts as curator) |
| Instance admin | possible | **never** |
| Personal space | yes | none |
| `admin_*` tools, space admin tools | with the role | never |
| Can be locked at once | yes | yes, together with all its tokens |
| Write limit | `policy.rate_limit_writes_per_minute` | `policy.rate_limit_service_writes_per_minute` (default 600) |

The account has the address `<name>@service.invalid`, a name that cannot receive mail. In `admin_list_users` it shows with `kind: service`. In the audit log its calls carry the client `token:<name>`, whatever the tool calls itself.

## Issuing an account and a token

A token is a secret. It is therefore **created only on the server**, with a command, and never by an assistant: what an assistant shows ends up in the chat and in the context of a model. The command runs next to the server and needs the same `config.yaml`. With the compose setup:

```bash
docker compose exec server atrium-tablinum -config /etc/atrium-tablinum/config.yaml \
  token create --name docs-sync --responsible maria@example.com
```

- **`--name`** is 2 to 40 characters: lower case letters, digits and hyphens.
- **`--responsible`** is the person who answers for the account. They must have an active account here (somebody who has logged in once). They are the one to ask when the account misbehaves, and the one to hand it on when they leave.
- **`--days`** is how long the token is valid: 90 by default, at most 365. A token always ends.
- **`--token-name`** names the token (default: a name made from the time), so that you can tell tokens of one account apart.

The command prints the token **once**, on its own line, starting with `atr_`. Store it right away as a secret, for example in the CI of the repository. Nothing in Atrium can show it again: only a hash is kept. If it is lost, issue a new token and revoke the old one.

Creating an account needs the Pro feature `service_accounts`. The account **starts without any rights**.

## Giving it rights

A new account sees nothing. Give it a role with the usual tool, with its address:

*"Add docs-sync@service.invalid as author in the space Code."* (`space_add_member`)

- Use the lowest role that does the job: **author** writes, replaces and archives its own entries, which is all a sync needs in a space without approval. **Curator** is needed only in a space that requires approval, if the tool is to replace or archive entries that are approved already. The role is capped at curator in any case.
- In a space that **requires approval**, new entries are drafts that a curator of the space releases. An author account cannot replace entries that are approved already; a curator account can. Decide per space what the tool may do.
- The default role of a space does not apply to the account: it never gets access to a space just because everybody does.
- Give it a role in **one space** and no more, one account per repository. A leaked token then reaches one space.

## Managing accounts and tokens

Instance admins manage the accounts with the assistant (`admin_service_accounts`):

- *"Show the service accounts."* (`list`) lists every account with its state, the person who answers for it, the spaces it holds a role in (with the capped role) and its tokens: name, valid, expired or revoked, expiry, last use. A token that ends within 30 days is marked. The tokens themselves are never shown.
- *"Lock the service account docs-sync."* (`lock`, after your confirmation) stops it at once: none of its tokens works. *"Unlock docs-sync."* (`unlock`) brings the valid tokens back.
- *"Revoke the token first of docs-sync."* (`revoke_token`) ends one token for good.
- *"Maria leaves; Peter answers for docs-sync from now on."* (`set_responsible`)

Locking, unlocking and revoking always work, with or without the licence feature. All of it is recorded in the audit log (`admin_service_account_lock`, `admin_service_account_unlock`, `admin_service_token_revoke`, `admin_service_account_responsible`).

When the person who answers for an account leaves, `list` says so (*the person is locked or gone*): name another person with `set_responsible`, best before the person leaves. The entries that a sync keeps up to date name a person as responsible too (`owner` in the configuration); `reassign_owner` hands those on.

## Rotating a token

Tokens end, and a token that may have leaked must go. Rotate without a gap:

```bash
docker compose exec server atrium-tablinum -config /etc/atrium-tablinum/config.yaml \
  token rotate --name docs-sync
```

This adds a second token and prints it once. **The old token keeps working**: switch the tool to the new one, then revoke the old one:

```bash
docker compose exec server atrium-tablinum -config /etc/atrium-tablinum/config.yaml \
  token revoke --name docs-sync --token-name first
```

At most three valid tokens per account can exist at once; a fourth is refused until one is revoked. `token list` shows accounts and tokens on the server, without secrets. Issuing a new token needs the feature `service_accounts`; revoking never does.

## Keeping a Git repository in Atrium

Many code repositories hold Markdown files between the code: architecture notes, runbooks, API descriptions. Atrium can keep them up to date so that assistants find them next to the rest of the knowledge, without anybody copying texts by hand.

A small client does the work. It is part of the Atrium image (command `sync`), reads the files of a directory, compares them with what Atrium holds and brings Atrium up to date. It runs in a CI job or on a schedule, with the token of a service account, and needs nothing else.

**1. Prepare Atrium.** Create an account for the repository, give it the **author** role in the target space, and keep the token as a secret in the CI of the repository (`ATRIUM_TOKEN`).

**2. Describe the repository.** Put a file `.atrium.yaml` into the repository:

```yaml
url: https://atrium.example.com        # or the environment variable ATRIUM_URL
space: code                            # the space the entries go to (slug)
key_prefix: "git:myrepo:"              # starts the key of every entry; the path of the file follows
origin_base: https://github.com/org/myrepo/blob/main/   # where the originals can be read
include: ["docs/**/*.md", "README.md"] # which files to take
exclude: ["**/draft/**"]
type: documentation                    # knowledge type of the entries (optional)
owner: maria@example.com               # the person responsible for the entries (optional)
tags: [code]                           # added to every entry (optional)
```

- **`key_prefix`** is what tells the entries of this repository from other entries in the space. Choose it once and keep it: the key (prefix and path) is how an entry is found again.
- **Patterns** are paths relative to the directory: `*` stands for one name, `**` for any number of folders, `?` for one character. The folder `.git` is never entered, and links are not followed.
- **Title, tags and type** come from the front matter of a file if it has one (`title`, `tags`, `type` between `---` lines), otherwise the title is the first `# ` heading, or the file name.
- **`owner`** names a human who answers for the entries. A service account cannot be the responsible person of an entry.
- The address must be `https`. Plain `http` is accepted only for `localhost`, so that a token never travels in the clear.

**3. Run it.** In a CI job on every push to the main branch, with the image of your Atrium version:

```bash
docker run --rm --user "$(id -u):$(id -g)" -v "$PWD:/repo:ro" \
  -e ATRIUM_TOKEN -e ATRIUM_URL \
  ghcr.io/behringer24/atrium-tablinum:<version> sync --dir /repo
```

`--user` lets the client read files that belong to the CI user. As a GitHub Actions step:

```yaml
- name: Update Atrium
  env:
    ATRIUM_TOKEN: ${{ secrets.ATRIUM_TOKEN }}
    ATRIUM_URL: https://atrium.example.com
  run: |
    docker run --rm --user "$(id -u):$(id -g)" -v "$GITHUB_WORKSPACE:/repo:ro" \
      -e ATRIUM_TOKEN -e ATRIUM_URL \
      ghcr.io/behringer24/atrium-tablinum:<version> sync --dir /repo
```

Pin the version of the image, as for the server. The token is read from the environment only, never from a flag, so that it does not show up in a process list.

**What the sync does**

| File | Result in Atrium |
|---|---|
| new | a new entry with the key |
| changed | a new version of the same entry, not a second entry |
| unchanged (same hash) | nothing is written, not even a version |
| removed or renamed | its entry is **archived**, never deleted |
| back again | the archived entry is restored, with the new text |

Each entry gets a reference to its original (`origin_base` plus the path) with the hash of the file, so a reader and an assistant can see where it comes from. The sync is safe to run again and again.

**Flags:** `--dir DIR` (default `.`), `--config FILE` (default `DIR/.atrium.yaml`), `--dry-run` (shows what it would do and changes nothing), `--no-archive` (never archives), `--allow-mass-archive`. The exit status is 0 if everything is as it should be, 1 if something failed (the output names the file) and 2 for a wrong call.

**Protection against a wrong directory.** If no file matches, or if more than half of the entries (at least five) would be archived, the sync archives **nothing** and fails with a message. That is much more often a wrong pattern or an empty checkout than a clean-out. If it is a clean-out, run once with `--allow-mass-archive`.

**Spaces with approval.** New entries are drafts there, whatever the account's role, and a person who is curator of the space releases them once (`review_knowledge`). After that an **author** account cannot replace an entry that is approved: the sync reports that for the file and goes on. A **curator** account replaces approved entries directly, so later changes of the files go live without a second look. Decide for each space whether the text that comes straight from a repository may do that; a space where it may not should not be fed by a curator account.

## What people see

An entry that a sync keeps up to date says so: `get_knowledge` shows `managed_by` (the account) and `sync_key`, and its source is `token:<account>`. **Nobody else changes or moves it**, space admins and instance admins included: *"This entry is kept up to date from an outside source: change the source and let the sync run again."* The text of the entry would be overwritten by the next run anyway. The refusal is recorded in the audit log (`denied`). **Archiving stays possible** for those who may archive, so that nobody is stuck with a wrong entry. Mind that the next sync **restores** it as long as the file is still in the repository: to retire an entry for good, remove the file or exclude it with a pattern.

Search, review dates and the rest work as for any entry. Entries from a repository are data like all others: the notices that tell assistants not to follow instructions in them apply.

## Limits and security

- **A token is a secret with the rights of its account.** The damage of a leak is limited by the role cap (curator at most, never admin), by one account per repository with a role in one space, by the term (at most 365 days, default 90), by the write limit, and by `lock` and `revoke_token`, which act at once.
- **Tokens start with `atr_`**, so that a secret scanner finds a leaked one in a repository. Rotate regularly and revoke what you no longer use. `last_used` in the list shows tokens that nobody uses.
- **The secret in the CI is long-lived.** Use the secret store of the CI, never the repository itself, and give the job nothing else that it does not need.
- **The write limit** of a service account is its own (`policy.rate_limit_service_writes_per_minute`, default 600 a minute, 0 = unlimited), so that a first sync of many files goes through without using the limit of people. A client that is asked to slow down waits a minute and tries again.
- **Entries are limited as for everybody:** the size of an entry (`policy.max_document_bytes`) and the quotas of the space. A file that is too large is reported and left out; the others go through.
- **Not a login for people.** A token reaches only `/mcp` and what is served with the same checks. It does not give a person a way around the identity provider.
- The audit log shows what a service account did (`token:<name>`), like any user.

## When the licence ends

Nobody is locked out and nothing is deleted. **Existing accounts and tokens keep working until each token's own expiry**, and a sync that runs keeps running. Without the feature `service_accounts` in the licence you can still lock and unlock accounts, revoke tokens, name another responsible person and read the list. What needs the feature is what is **new**: creating an account and issuing a token, a rotation included. A token that ends after the licence does ends the automation then, unless a licence with the feature is installed again and a new token is issued: the list marks tokens that end within 30 days and says whether the licence covers issuing a new one.

## Troubleshooting

| Problem | What to try |
|---|---|
| `Issuing a token needs a Pro licence with the feature "service_accounts"` | The licence does not include the feature (or has ended). `admin_license` shows the state |
| `the responsible person must be an active person` | The address is unknown or the person is locked. They must have logged in once |
| `an account or a token with that name exists already` | Use `token rotate` for a new token of an existing account, or another `--token-name` |
| `the account has 3 valid tokens already` | Revoke one (`token revoke`) |
| The sync fails with `connection` | Wrong or expired token, wrong address, or the account is locked. `list` shows the state; the server answers the same to all of them |
| `The space "..." is unknown or this account has no author role in it` | Give the account the author role in the space (`space_add_member` with its address) |
| `approved entries in spaces that require approval can currently only be changed by curators` | The space requires approval and the account is an author: give it the curator role, or release the drafts |
| `no file matches, but N entries exist` | Check `include` and `--dir`, or use `--allow-mass-archive` if the entries really are gone |
| `this entry is kept up to date from an outside source` | A person tried to change a synced entry: change the file in the repository |
| `the token would be sent unencrypted` | `url` is plain `http` to a host other than localhost: use `https` |
| A file is skipped with a reason | The output names it: not UTF-8, empty, larger than 1 MB, front matter that is not valid YAML |

## FAQ

**Can an assistant create a token for me?** No, by design. A token shown in a chat would be in the transcript and in the context of a model. Create it with the command on the server.

**Can I see a token again?** No. Only a hash is stored. Issue a new token and revoke the old one.

**Does a service account count against the user limit?** No. The limit counts people.

**Can a service account be an instance admin, or admin of a space?** No. Never an instance admin, and in a space at most curator, whatever membership it was given.

**Can the account read my personal space or the spaces open to everybody?** No. It has no personal space, and the default role of a space does not apply to it. It reads and writes only where somebody gave it a role.

**Several repositories, one space?** Use one account per repository and a different `key_prefix` for each. Each account keeps and archives only its own entries.

**Can two repositories write the same key?** No. A key belongs to the account that wrote it first; another account that uses it is refused.

**What happens to the entries when I switch the sync off?** They stay, as they are, and stay protected as synced entries until you archive them. Archive them with the usual tools (`archive_knowledge`), or delete them (`delete_knowledge`, space admin) if they must go.

**Can the sync run on a schedule, not on every push?** Yes: run the same command from any scheduler. It writes only what changed.

**Is the sync a second login?** No. It talks to the same `/mcp` endpoint as an assistant, with a token, and every call is checked like any other.

**Does it work with another version control system than Git?** The client reads a directory; how the files get there does not matter. The `origin_base` just has to be an address where the originals can be read.

**Does it need an internet connection from the server?** No. The client connects to Atrium; Atrium never calls the repository.
