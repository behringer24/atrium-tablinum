# Releases and image

A release is a tag of the form `vX.Y.Z` on this repository, also as a pre-release (`v1.2.0-rc1`). The workflow `.github/workflows/test.yml` runs `gofmt`, `go vet`, `go test ./...` against a Postgres with pgvector and `govulncheck` for every tag and every push to `dev`. The container images are built and published by the maintainer's build pipeline once these checks have passed, for `linux/amd64` and `linux/arm64`, as `ghcr.io/behringer24/atrium-tablinum`.

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

The progress of the tests is visible under "Actions" in this repo. The finished package appears under "Packages" and is linked to the repo through the label `org.opencontainers.image.source`.

## Release lines and the licence

Every build knows the release line it belongs to from `internal/product/line.txt` (embedded; the line, the day it was released, optionally an epoch). A licence covers builds whose line is dated on or before its `maintenance_until`, so patch releases of a line carry the date of `X.Y.0`. **When a new line starts, set `line` and `date` in the commit that gets the tag `vX.Y.0`; a patch branch keeps the file and a backport never takes it along.** Forgetting it would give the new line away to every customer of the previous one. The image build also passes the commit date (`BUILD_DATE`), shown by `server_info` for support and never used for licensing. Details: [licensing](licensing.md).

## Canary builds

Every push to the `dev` branch also yields a canary image, published only as

| Image tag | Meaning |
|---|---|
| `canary` | moves with every push to `dev` |
| `canary-<short sha>` | immutable, one per build; use it to pin or to roll back |

A canary build never gets `latest` or a version tag, so it cannot reach anyone who follows releases. `server_info` reports `canary-<short sha>` as the version. Only the newest few canary images are kept: to roll back, use one of the last `canary-<short sha>` tags.

Flow: work happens on `feat/...`, `fix/...` and `doc/...` branches. A branch is merged into `dev` to try it on a test server; what proved itself goes from `dev` to `main`, and a release is a tag on `main`.

Point a test server at the moving tag and pull before each restart:

```yaml
image: ghcr.io/behringer24/atrium-tablinum:canary
pull_policy: always
```

```bash
docker compose pull && docker compose up -d
```

Things to know:

- Migrations only go forward. A server that ran a canary build must not simply go back to a release with an older schema: use a test database or take a backup first (`scripts/backup.sh`).
- Every build leaves a `canary-<short sha>` version behind, plus one untagged entry per platform (amd64, arm64) that belongs to it. Those untagged entries are the per-platform images of the tagged one, not garbage: never delete them by hand or with a rule for "all untagged", the tagged image would stop working.
- A canary build is untested by users. Run it on a test server, not where people work.

## Using the image

In the Compose file, instead of `build: .` put the address with a fixed version, e.g. `ghcr.io/behringer24/atrium-tablinum:0.1.0`. A fixed version is better than `latest` because an update then happens deliberately.

If pulling needs a login, use a token (personal access token classic with `read:packages`):

```bash
echo "$TOKEN" | docker login ghcr.io -u <user> --password-stdin
```

## Supply chain

What the build depends on is pinned and checked:

- **Go toolchain:** `go.mod` names the patch release (`toolchain go1.26.10`), so a local `go` and the CI use the same one, and the Dockerfile builds with `golang:1.26.10-alpine`. The standard library is part of what you ship: update the patch release when `govulncheck` reports a fix.
- **Base images** are pinned by tag and digest (`golang:1.26.10-alpine@sha256:...`, `alpine:3.22@sha256:...`). To update one, take the new digest with `docker buildx imagetools inspect <image>`.
- **GitHub Actions** are pinned to commit SHAs, with the tag in a comment. A moved tag in an action repository cannot run other code in the build. Get a SHA with `git ls-remote --tags https://github.com/<owner>/<repo> refs/tags/<tag>`.
- **`govulncheck`** runs in the test job of `test.yml` and fails the build when a known vulnerability is reachable from the code. Run it locally with `go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...`.
- **Dependabot** (`.github/dependabot.yml`) opens weekly pull requests for Actions, Go modules and base images against `dev`, so that an update is tried as a canary build before it reaches `main`. Review them like any change; the tests and `govulncheck` run on them. Security updates that GitHub opens for a known vulnerability always target `main`.

## Notes

- Only the server image is published. Postgres and TEI still come from their vendors.
- The server image is also available for arm64. The TEI server is not: for ARM hosts, embedding must run through an API backend (`openai` or `bedrock`, see [embedding.md](embedding.md)).
- Rolling back: a published image remains available under its tag. To go back, point the Compose file to the earlier version.
