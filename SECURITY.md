# Security policy

## Reporting a vulnerability

Please report a suspected vulnerability privately, not in a public issue or pull request.

Use GitHub's private reporting: open the **Security** tab of the repository
([behringer24/atrium-tablinum](https://github.com/behringer24/atrium-tablinum)) and choose **Report a vulnerability**,
or go straight to <https://github.com/behringer24/atrium-tablinum/security/advisories/new>.

Please include the version (`server_info`), what you did, what you expected and what happened. Do not include real
customer data. Test only against your own installation.

## What to expect

- We confirm the receipt of a report within five working days.
- We assess it, tell you whether we consider it a vulnerability, and agree on a date for the disclosure with you.
- A fix ships as a patch release; the [changelog](CHANGELOG.md) names it, and for a vulnerability of some weight a security
  advisory is published together with it.
- We credit you in the advisory if you wish.

## Supported versions

Security fixes go into the newest release line (the line `server_info` reports) and, where it is practical, the one before it.
Run the newest patch release of your line.

## For operators

- [docs/incident.md](docs/incident.md) is the runbook for an incident in a running instance (a compromised account, leaked
  tokens, harmful content, a suspected breach of the server).
- [docs/deployment.md](docs/deployment.md) describes the hardened setup, including the separate database accounts.
- Manufacturers of products with digital elements have reporting duties under the EU Cyber Resilience Act; operators who
  integrate Atrium into such a product should report to us any vulnerability they find in it.
