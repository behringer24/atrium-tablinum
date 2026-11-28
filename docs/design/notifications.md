# Notifications

**Status:** built (see the [changelog](../../CHANGELOG.md)). This note records the decisions and their reasons.

Without notifications nobody is told anything: curators find waiting drafts only by asking, authors learn of an approval or a return only by looking, and an administrator sees a failing embedding service only when they open `admin_health`. For an organisation that works through an assistant this is a real gap, because the assistant is only consulted when somebody thinks of asking.

The first step is small on purpose: a few events, two ways to deliver them (a web hook and e-mail), one place that decides where an event goes. Anything beyond one global set of connections and events is left out.

## Principles

- **Off by default.** Without `notifications.enabled` in `config.yaml` nothing is sent and nothing is stored. Each connection is switched on by an instance admin, one by one.
- **A notice points, it does not tell.** A message says that something waits ("3 drafts wait for review in the space Operations."). The footer of a mail tells the person to ask their assistant. A notice carries no title, no text, no review note. Atrium is assistant-first: there is no page to link to, and the rights check happens when the person asks, not when the notice is written.
- **No metadata for people who may not see it.** A chat channel or a mailbox is another system with its own readers. A message to a web hook names a space only when an instance admin set that connection to (`space_names`); a mail goes to people who are curators or authors of the space, so it names it.
- **Never lose, rarely repeat.** An event is written in the same transaction as the change that caused it. Delivery is at least once, with retries; a receiver can drop repeats by the delivery id.
- **The connections stay in the configuration.** An address of a chat system is a secret (whoever knows it can post), and so is an SMTP password. Connections live in `config.yaml` as `${ENV_VAR}` references, never in the database, and no tool shows an address or creates or changes a connection. The tool only chooses among them.

## Events

| Event | When | Who is told (mail) |
|---|---|---|
| `draft_waiting` | an entry or a skill enters review (a new draft, an author's change after a return, an entry moved into or restored in a space with approval) | the curators and admins of the space, by their effective role |
| `draft_approved` | a curator approves a draft | its author |
| `draft_returned` | a curator returns a draft with a note | its author (the note is not part of the notice) |
| `item_received` | somebody sends an entry or skill to a personal space (see [sharing](../sharing.md)) | the owner of that space |
| `health_changed` | a check of `admin_health` goes from ok to failing or back | the instance admins |

- **Nobody is told of their own action.** The person who caused an event is left out.
- **Locked and service accounts get nothing.**
- **Roles are looked up when the event is routed**, with the same code as every other right (`authz`), so groups, inherited roles and instance admins are respected.
- **Approvals in a personal space are not announced** to the sender: they have no access to the copy.
- Not in this step: reminders that a review date has come, a feed of every change for other systems, and anything about logins or sessions.

## Connections

A connection is a web hook or an SMTP server, set in `config.yaml`:

```yaml
notifications:
  enabled: true
  allow_private_networks: false
  connections:
    - name: curators-chat
      type: slack            # slack | webhook | smtp
      url: ${SLACK_WEBHOOK_URL}
    - name: automation
      type: webhook
      url: ${AUTOMATION_URL}
      secret: ${AUTOMATION_SECRET}   # signs the body, webhook only
    - name: mail
      type: smtp
      host: mail.example.org
      port: 587
      tls: starttls          # starttls | tls | none (none for a server on this host only)
      username: ${SMTP_USER}
      password: ${SMTP_PASSWORD}
      from: atrium@example.org
```

Validation refuses a name that is not lower-case letters, digits, `-` and `_`, a name used twice, an address that is not HTTPS or carries a login, a Slack address that is not `hooks.slack.com`, a web hook secret under 16 characters, an SMTP connection without a sender or with a user but no password, and `tls: none` for a remote server.

**Which connections are used.** The build decides how many it uses; this edition uses one web hook (`slack` or `webhook`) and one SMTP connection, the first of each kind in the order of the file. The others may stay in the file; `admin_notifications` and `admin_health` say they are not used.

### What an instance admin chooses at runtime

The tool `admin_notifications` (instance admins):

- `get` lists the connections (name, type, state, events, mode, figures, last error), the events, and the connections that are not used. Never an address.
- `set connection=... events=[...] enabled=... mode=... digest_at=... space_names=...` needs `confirm=true` after the user was told the new values. A connection is on only with at least one event. The choice is stored in `notification_settings` and `notification_routes` and applies to the next notice, without a restart. It is audited as `admin_notifications_set`.
- `test connection=... confirm=true` sends a test notice (a mail goes to the caller's own address) and is audited as `admin_notifications_test`.

A connection without a choice sends nothing.

### Web hooks

- **`webhook`:** a JSON body with the delivery id (`id`), `type`, `created_at`, `instance`, `count`, `detail` (for a health check: its name and state), `message` and, if set, `space`. No titles. Headers `X-Atrium-Delivery`, `X-Atrium-Timestamp` and `X-Atrium-Signature: sha256=<hex>`, an HMAC-SHA256 of `timestamp + "." + body` with the secret. A receiver rejects old timestamps and repeated ids.
- **`slack`:** the format of a Slack incoming web hook, a single `text` field. No signature (Slack checks none); the address is the secret.
- Other chat systems (Teams, Mattermost) take the `webhook` form through their own automation.

### E-mail

- **Recipients come from roles, not from a list**, as in the table above. The address is the one the person logged in with. A mail is plain text.
- **Instant or digest.** An instant notice waits one minute for more of its kind and goes out as one message. A digest goes out once a day at `digest_at` (UTC, default 08:00). Everything due for one person goes into one mail.
- **No titles.** A mail says "3 drafts wait for review in the space Operations" and asks the person to talk to their assistant. Header values are cleaned of line breaks.

## Outbox and delivery

Two tables are written by the changes and read by the delivery task:

- **`notification_events`**: `kind`, `space_id`, `subject_id`, `subject_kind`, `actor_id`, `recipient_id`, `detail`. No title, no text: a notice names what happened, never what it is.
- **`notification_deliveries`**: one row per connection, kind, space, recipient and detail. `status` is `pending`, `sending`, `sent` or `failed`; `count` is how many events were merged; `attempts`, `send_after`, `last_error`.

The job queue in `jobs` is not used: it has no time of the next attempt and no way to merge rows.

- **Emit.** `notify.Emit` writes the event inside the transaction of the change, next to the audit record, and writes nothing while notifications are off. An error rolls the change back, like the audit record.
- **Route.** A periodic task (every 10 seconds) turns new events into deliveries through a `Router`. The built-in router gives each connection that is on the events chosen for it. A new event is merged (`count + 1`) into a delivery of the same connection, kind, space, recipient and detail that has not been sent yet.
- **Send.** The same task claims due deliveries (`FOR UPDATE SKIP LOCKED`, a lease of five minutes so that a crash leaves them to be claimed again), sends them and marks them. Retries wait 30 seconds, 2 minutes, 10 minutes, 1 hour and 6 hours; after six tries a delivery is `failed`. `429` is honoured with its `Retry-After`. `404`, `410`, other `4xx` and `3xx` answers are not retried: the address was probably revoked. A web hook gets at most 20 calls per connection and run, so that a slow target cannot hold up the others.
- **Clean up.** Sent and failed deliveries and routed events are deleted after 30 days.

The `Router` is a small interface: it takes an event and returns the deliveries to make (connection, recipient, time). Emitting and delivery know nothing about how a connection was chosen, so the routing can change without touching either.

## Security

- **HTTPS only** for web hooks. The address is checked when the connection is made, on the address the name resolved to: loopback, private, link-local (which includes the metadata address of cloud hosts), shared address space and anything that is not a public unicast address are refused. Redirects are not followed and no proxy is used. A chat system inside the own network needs `notifications.allow_private_networks: true`, set knowingly.
- **Errors leak no address.** The text of a network error is cut down to its cause before it is stored or shown, because the address of a Slack web hook is its secret.
- **Limits.** Ten seconds to connect, fifteen for a call, the answer read only up to a few kilobytes and then dropped.
- **SMTP** needs TLS (STARTTLS or from the first byte, TLS 1.2 or newer) unless the server is on this host. A login is offered only over TLS or to a local server.
- **No tool creates or changes a connection.** Connections are a matter of the server's configuration, like the login providers. A tool that created them would let a stolen admin session send data to an address of the attacker's choice.
- **Visible when broken.** `admin_health` has a check `notifications`: it says how many connections are on and how many notices wait, and turns `degraded` when notices were given up in the last day or have waited more than 15 minutes with an error. `admin_notifications get` shows the same per connection. A broken connection never blocks the change that caused the event.
- **The support bundle** lists connection names and types, never an address or a secret.

## Where the code is

- `internal/notify`: events, the two tables, settings, the router and the delivery task, the web hook and SMTP senders, the health watcher.
- Emitting: the write paths of `internal/docs` (store, update, move, restore, review, send), `internal/skills` (save, review, send) and `internal/share`.
- `internal/config/notifications.go`: the section and its validation; `internal/edition`: how many connections the edition uses.
- `internal/mcp/tools_notify.go`: `admin_notifications`.
- `cmd/server/main.go`: wiring, the periodic tasks `notifications` and `health_watch`, the health check.

## Not done, on purpose

- **Nothing beyond one global set of connections and events.** That is enough to make the review workflow live, and it is easy to explain.
- **No direct messages in a chat system.** That needs an app registration and more rights than a channel web hook. A channel addresses roles ("the curators"), which is what the first step can say.
- **No titles in a notice.** A mailbox is another system. If a customer asks, a title would be added only for a recipient who may see the entry at the moment of sending (the visibility clause of search).
- **No inbound web hooks and no feed of every change.** Those belong with the interface for other systems (see the roadmap).
- **No push to the assistant.** An MCP client reads when it asks. `whats_new` stays the way to catch up.
