# Notifications

**Status:** design, not built. This note records the decisions and their reasons before the first line of code.

Today nobody is told anything. Curators find waiting drafts only by asking, authors learn of an approval or a return only by looking, and an administrator sees a failing embedding service only when they open `admin_health`. For an organisation that works through an assistant this is a real gap: the assistant is only consulted when somebody thinks of asking.

The first step is small on purpose: a few events, two ways to deliver them (a web hook and e-mail), one place that decides where an event goes. Anything beyond one global set of targets and events is left out.

## Principles

- **Off by default.** Without a `notifications` section nothing is sent and nothing is stored. Each delivery type is switched on by itself.
- **A notice points, it does not tell.** A message says that something waits and where to look ("3 drafts wait in the space Operations. Ask your assistant to show the drafts to review."). It does not carry content. Atrium is assistant-first: there is no page to link to, and the rights check happens when the person asks, not when the notice is written.
- **No metadata for people who may not see it.** Chat channels and mail systems are other systems with their own readers. The roadmap rule stands: a notification must not carry anything to people who may not see the entry.
- **Never lose, rarely repeat.** An event is written in the same transaction as the change that caused it. Delivery is at least once, with retries; receivers can drop repeats by the delivery id.
- **Secrets stay in the configuration.** Web hook addresses of chat systems are secrets (whoever knows the address can post). They live in `config.yaml` as `${ENV_VAR}` references, never in the database, and no tool returns them.

## Events

| Event | When | Who is told |
|---|---|---|
| `draft_waiting` | an entry or a skill enters review (new draft, or an author's change after a return) | the curators and admins of the space |
| `draft_approved` | a curator approves a draft | the author |
| `draft_returned` | a curator returns a draft with a note | the author (the note is not part of the notice, the assistant shows it) |
| `item_received` | somebody sends an entry or skill to a personal space (see [sharing](../sharing.md)) | the owner of that space |
| `health_changed` | a check of `admin_health` goes from ok to failing or back | the instance admins |

Not in this step: reminders that a review date has come (see the roadmap), changes to entries as a feed for other systems, and anything about logins or sessions.

## Event and delivery

Two tables, written by the change and read by the delivery task:

- **`notification_events`**: `id`, `kind`, `space_id`, `subject_id` (entry or skill), `subject_kind`, `actor_id`, `created_at`. No title, no text: the facts are looked up when a message is made, with the rights of the recipient.
- **`notification_deliveries`**: one row per event, target and recipient. `id` (the delivery id sent to receivers), `event_id`, `target` (the configured name), `recipient_id` (for mail), `status` (`pending`, `sent`, `failed`), `attempts`, `send_after`, `last_error`, `count`, `sent_at`.

The queue in `jobs` is not used: it has no time of the next attempt and no way to merge rows.

**Delivery** is a periodic task (every 10 seconds) that claims due rows with `FOR UPDATE SKIP LOCKED`, as the job queue does. Retries wait 30 seconds, 2 minutes, 10 minutes, 1 hour and 6 hours; after that the row is `failed`. `429` is honoured with its `Retry-After`. A refused address (`404` or `410` from a chat system) is not retried at once but counted as a broken target.

**Merging.** A bulk import by seven agents must not send seven hundred messages. A pending row for the same kind, space, target and recipient absorbs the next event by raising its `count` (the first row's `send_after` is not moved). One message then says "12 drafts". Sent and failed rows are deleted after 30 days by a periodic task.

## Targets

Targets are listed in `config.yaml`; changing them needs a restart. A target is either a web hook or e-mail.

```yaml
notifications:
  enabled: false
  webhooks:
    - name: curators-chat
      kind: slack            # slack | generic
      url: ${SLACK_WEBHOOK_URL}
      events: [draft_waiting, health_changed]
    - name: automation
      kind: generic
      url: ${AUTOMATION_URL}
      secret: ${AUTOMATION_SECRET}   # signs the body, generic only
      events: [draft_waiting, draft_approved, draft_returned, health_changed]
  email:
    smtp_host: mail.example.org
    smtp_port: 587
    tls: starttls            # starttls | tls | none (none only for localhost)
    username: ${SMTP_USER}
    password: ${SMTP_PASSWORD}
    from: atrium@example.org
    events: [draft_waiting, draft_approved, draft_returned, item_received, health_changed]
    mode: instant            # instant | digest
    digest_at: "08:00"       # UTC, one mail per person and day
    include_titles: false
```

### Web hooks

- **`generic`:** a JSON body with the delivery id, kind, time, instance name, space name and count; no titles. Headers `X-Atrium-Delivery` (the id), `X-Atrium-Timestamp` and `X-Atrium-Signature: sha256=<hex>`, an HMAC of `timestamp + "." + body` with the secret. A receiver rejects old timestamps and repeated ids.
- **`slack`:** the format of a Slack incoming web hook, a single `text` field with the notice. No signature (Slack does not check one); the address is the secret. The host must be `hooks.slack.com`.
- **Space names are metadata too.** A channel may have readers who are not in the space. `webhooks[].space_names` (default `false`) decides whether a message names the space or only says "a space".
- Other chat systems (Teams, Mattermost) take the generic form through their own automation.

### E-mail

- **Recipients come from roles, not from a list.** For `draft_waiting` the curators and admins of the space; for `draft_approved` and `draft_returned` the author; for `item_received` the owner of the personal space; for `health_changed` the instance admins. Locked accounts are skipped. The address is the one the person logged in with.
- **Titles are off by default** (`include_titles`). When on, a title is added only if the recipient can see the entry at the moment of sending (the same visibility clause as search), and it is made safe to show like every title elsewhere.
- **Instant or digest.** Instant mails are merged as described above. A digest is one mail per person and day with counts per space and kind.
- **A mail names no secrets and carries no links with tokens.** It says what to ask the assistant.

## Security

- **HTTPS only** for web hooks. The address is resolved when the connection is made, and loopback, link-local, private and cloud metadata addresses are refused, also after a redirect; redirects are not followed at all. Customers who run a chat system inside their own network set `notifications.allow_private_networks: true` knowingly.
- **Limits.** Ten seconds per request, the response read only up to a few kilobytes and then dropped, and no more than a fixed number of deliveries per minute per target, so that a defective target cannot hold up the others.
- **SMTP** needs TLS unless the host is local. The password comes from the environment.
- **No tool changes a target.** Targets are a matter of the server's configuration, like the login providers. A tool that creates targets would let a stolen admin session send data to an address of the attacker's choice.
- **What happens when a target is broken** is visible: `admin_health` gets a check `notifications` that lists targets (by name, never by address) with the number of failed and pending deliveries and the age of the oldest one. A broken target never blocks the change that caused the event.

## Where the code goes

- `internal/notify`: the events, the two tables, the delivery task and the two senders. Emitting an event is one call inside the transaction of the change (`notify.Emit(ctx, tx, event)`), placed next to the audit record in the write paths of `internal/docs`, `internal/skills`, `internal/share` and in the health summary.
- **Routing sits behind a small interface.** `Router` takes an event and returns the deliveries to make (target, recipient). The built-in router reads the configured lists above. Emitters and delivery know nothing about how a target was chosen, so that the routing can change without touching either.
- `internal/config`: the section above with its validation (known kinds and events, HTTPS, one secret per generic target, no secret written into the file itself).
- `admin_health`: the `notifications` check. `config.example.yaml`, `docs/user/05-instance-admin.md`, `docs/approval.md`, `docs/user/02-author.md` and `03-curator.md` (the places that now say "there is no notification"), changelog and roadmap change with the first code.

## Not done, on purpose

- **Nothing beyond one global set of targets and events.** That is enough to make the review workflow live, and it is easy to explain.
- **No direct messages in a chat system.** That needs an app registration and more rights than a channel web hook. A channel addresses roles ("the curators"), which is what the first step can say.
- **No inbound web hooks and no feed of every change.** Those belong with the interface for other systems (see the roadmap).
- **No push to the assistant.** An MCP client reads when it asks. `whats_new` stays the way to catch up.

## Open questions

- The time zone of the digest. Recommendation: UTC in the configuration, as the other times of the server.
