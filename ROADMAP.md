# Roadmap

What is planned next and what is deliberately left open. The order within a section is not a commitment; commitments only exist as entries in the [CHANGELOG](CHANGELOG.md).

Ground rules: Atrium is offered commercially to companies of different sizes. Security comes before convenience, risky features are off by default, and one instance serves exactly one organisation.

## Next

- **Summarising with `replaces`:** `store_knowledge` gets a `replaces` field (ids of sources in the same space) and `confirm=true`. The summary is stored and the sources are archived in the same transaction. Provenance is recorded as `derived_from`, and the sources carry the note "summarised in ...". Whoever may not archive the sources stores the summary as a draft; the curator archives the sources when releasing it, and a source that changed since the request is left alone. Replacing across spaces is deliberately not supported.
- **Security, remainder of the audit of 2026-11-03:**
  - consent page before a new client is connected
  - audit log leaving the database (JSON line on stdout, Postgres role with `INSERT` only, optionally a hash chain) and a retention period
  - `SECURITY.md` with a reporting route, an incident runbook (`docs/incident.md`) and a tool that revokes all tokens of the instance
  - supply chain: pin dependencies and images, `govulncheck` and Dependabot in the build
  - abuse detection (unusually many reads, failed token checks) in `admin_health`
  - confirmation when storing into spaces of others
- **Documentation and operations:** after the rollout, the user manual in the space "Allgemein" is adjusted to the archive behaviour. The description of "Allgemein" and the instance context are set.
- **Language:** translate the existing German documentation and code comments to English (see the rule in the changelog).

## Later

- **Roles for customers with high protection needs:** instance admins are admin in every team space today and can read everything there. Some customers need a split between "manage users and operations" and "read content" (for example for management or HR). This includes separate roles, a waiting period with a second admin when transferring personal spaces, and notifications. Personal spaces are already closed to everyone except their owner.
- **Groups from the identity provider:** map groups to spaces, with protection against stale groups (never more than reader through groups, reconciliation at every login, visibility for admins).
- **Client ID Metadata Documents:** the client registration the OAuth specification prefers. Dynamic Client Registration works with Claude and ChatGPT today.
- **Reranker** for better search results and **re-embedding jobs** when the embedding model changes.
- **A draft next to the released version** and **notifications** in the approval workflow.
- **Privacy:** tool to anonymise users in the audit log.
- Test **Entra login** against a real tenant and **Bedrock** against a real AWS account.

## Scaling (not measured yet)

Statements about data volume and performance are estimates so far. Before making firm promises to customers:

1. Load test with synthetic corpora (100k, 1M, 10M chunks) and permission filters of varying selectivity, above all for filtered vector search.
2. Remove the bottlenecks of running several instances: role cache and rate limits per process (replace with `LISTEN/NOTIFY` and a shared counter), periodic jobs on one instance only (leader lock), separate pools for reads and writes, asynchronous audit, a cache for query vectors.
3. Operations guide for read replicas, load balancer and Postgres cluster.

## Deliberately not planned

- **Several customers on one instance.** An instance belongs to one organisation; separate customers get separate instances.
- **Heuristic detection of prompt injection** in the store. It would give false security. Instead, content is marked as data and risky actions need the user's confirmation.
