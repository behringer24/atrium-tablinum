# Releases and image

A release is created when a tag of the form `vX.Y.Z` is pushed, also as a pre-release (`v1.2.0-rc1`). The workflow `.github/workflows/release.yml` then does the following:

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

## Using the image

In the Compose file, instead of `build: .` put the address with a fixed version, e.g. `ghcr.io/<owner>/<repo>:0.1.0`. A fixed version is better than `latest` because an update then happens deliberately.

The package is created as **private**. Anyone who is to pull it needs access (package settings, "Manage access") and logs in with a token (personal access token classic with `read:packages`):

```bash
echo "$TOKEN" | docker login ghcr.io -u <benutzer> --password-stdin
```

Making a package public cannot be undone. This should only happen once license and name are settled.

## Notes

- Only the server image is published. Postgres and TEI still come from their vendors.
- The server image is also available for arm64. The TEI server is not: for ARM hosts, embedding must run through an API backend (`openai` or `bedrock`, see [embedding.md](embedding.md)).
- Rolling back: a published image remains available under its tag. To go back, point the Compose file to the earlier version.
