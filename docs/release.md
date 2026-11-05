# Releases and image

A release is created when a tag of the form `vX.Y.Z` is pushed, also as a pre-release (`v1.2.0-rc1`). The workflow `.github/workflows/release.yml` (named "build", it also builds canary images, see below) then does the following:

1. **Tests:** `gofmt`, `go vet` and `go test ./...` against a Postgres with pgvector. If anything fails, no image is built.
2. **Image** for `linux/amd64` and `linux/arm64`, published to the GitHub Container Registry as `ghcr.io/<owner>/<repo>`.

| Tag in the repo | Image tags |
|---|---|
| `v1.2.3` | `1.2.3`, `1.2`, `1`, `latest` |
| `v0.4.0` | `0.4.0`, `0.4`, `latest` (no tag `0` for 0.x) |
| `v1.2.3-rc1` | `1.2.3-rc1` (no `latest`) |

## Publishing

```bash
git tag -a v0.1.0 -m "Atrium Tablinum 0.1.0"
git push origin v0.1.0
```

The progress is visible under "Actions" in the repo. The finished package appears under "Packages" and is linked to the repo through the label `org.opencontainers.image.source`.

## Release lines and the licence

Every build knows the release line it belongs to from `internal/product/line.txt` (embedded; the line, the day it was released, optionally an epoch). A licence covers builds whose line is dated on or before its `maintenance_until`, so patch releases of a line carry the date of `X.Y.0`. **When a new line starts, set `line` and `date` in the commit that gets the tag `vX.Y.0`; a patch branch keeps the file and a backport never takes it along.** Forgetting it would give the new line away to every customer of the previous one. The image build also passes the commit date (`BUILD_DATE`), shown by `server_info` for support and never used for licensing. Details: [licensing](licensing.md).

## Canary builds

Every push to the `dev` branch builds a canary image: the same tests and the same image as a release, but published only as

| Image tag | Meaning |
|---|---|
| `canary` | moves with every push to `dev` |
| `canary-<short sha>` | immutable, one per build; use it to pin or to roll back |

A canary build never gets `latest` or a version tag, so it cannot reach anyone who follows releases. `server_info` reports `canary-<short sha>` as the version.

Flow: work happens on `feat/...`, `fix/...` and `doc/...` branches. A branch is merged into `dev` to try it on a test server; what proved itself goes from `dev` to `main`, and a release is a tag on `main`. A newer push to `dev` cancels a canary build that is still running.

Create the branch once, from the current `main`:

```bash
git checkout main && git checkout -b dev && git push -u origin dev
```

Point a test server at the moving tag and pull before each restart:

```yaml
image: ghcr.io/<owner>/<repo>:canary
pull_policy: always
```

```bash
docker compose pull && docker compose up -d
```

Things to know:

- Migrations only go forward. A server that ran a canary build must not simply go back to a release with an older schema: use a test database or take a backup first (`scripts/backup.sh`).
- Every build leaves a `canary-<short sha>` version behind, plus one untagged entry per platform (amd64, arm64) that belongs to it. Those untagged entries are the per-platform images of the tagged one, not garbage: never delete them by hand or with a rule for "all untagged", the tagged image would stop working. Provenance attestations, which would add two more entries per build, are switched off.
- The workflow `cleanup.yml` removes old canary versions (`canary-*`, older than a week, keeping the newest 10) together with their orphaned platform images. Release tags are never selected. One-time setup: create a classic personal access token with the scopes `read:packages` and `delete:packages`, store it as the repository secret `PACKAGES_PAT`, then start the workflow by hand (Actions, "cleanup", Run workflow, `dry-run` on) and read the list of what it would delete. If it only names old canary versions, set `dry-run` in `cleanup.yml` to `'false'` for scheduled runs (it runs Mondays); a manual run with `dry-run` off deletes at once.
- A canary build is untested by users. Run it on a test server, not where people work.

## Using the image

In the Compose file, instead of `build: .` put the address with a fixed version, e.g. `ghcr.io/<owner>/<repo>:0.1.0`. A fixed version is better than `latest` because an update then happens deliberately.

The package is created as **private**. Anyone who is to pull it needs access (package settings, "Manage access") and logs in with a token (personal access token classic with `read:packages`):

```bash
echo "$TOKEN" | docker login ghcr.io -u <benutzer> --password-stdin
```

Making a package public cannot be undone. This should only happen once license and name are settled.

## Supply chain

What the build depends on is pinned and checked:

- **Go toolchain:** `go.mod` names the patch release (`toolchain go1.26.10`), so a local `go` and the CI use the same one, and the Dockerfile builds with `golang:1.26.10-alpine`. The standard library is part of what you ship: update the patch release when `govulncheck` reports a fix.
- **Base images** are pinned by tag and digest (`golang:1.26.10-alpine@sha256:...`, `alpine:3.22@sha256:...`). To update one, take the new digest with `docker buildx imagetools inspect <image>`.
- **GitHub Actions** are pinned to commit SHAs, with the tag in a comment. A moved tag in an action repository cannot run other code in the build. Get a SHA with `git ls-remote --tags https://github.com/<owner>/<repo> refs/tags/<tag>`.
- **`govulncheck`** runs in the test job of `release.yml` and fails the build when a known vulnerability is reachable from the code. Run it locally with `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`.
- **Dependabot** (`.github/dependabot.yml`) opens weekly pull requests for Actions, Go modules and base images. Review them like any change; the tests and `govulncheck` run on them.
- **`PACKAGES_PAT`** (used by `cleanup.yml`) is a classic personal access token with `read:packages` and `delete:packages` only, as GitHub's fine-grained tokens did not cover container packages when this was written. Give it an expiry, rotate it, and keep it only as a repository secret.

## Notes

- Only the server image is published. Postgres and TEI still come from their vendors.
- The server image is also available for arm64. The TEI server is not: for ARM hosts, embedding must run through an API backend (`openai` or `bedrock`, see [embedding.md](embedding.md)).
- Rolling back: a published image remains available under its tag. To go back, point the Compose file to the earlier version.
