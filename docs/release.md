# Releases und Image

Ein Release entsteht, wenn ein Tag der Form `vX.Y.Z` gepusht wird, auch als Vorabversion (`v1.2.0-rc1`). Der Workflow `.github/workflows/release.yml` macht dann:

1. **Tests:** `gofmt`, `go vet` und `go test ./...` gegen einen Postgres mit pgvector. Schlägt etwas fehl, entsteht kein Image.
2. **Image** für `linux/amd64` und `linux/arm64`, veröffentlicht in der GitHub Container Registry als `ghcr.io/<owner>/<repo>`.

| Tag im Repo | Image-Tags |
|---|---|
| `v1.2.3` | `1.2.3`, `1.2`, `1`, `latest` |
| `v0.4.0` | `0.4.0`, `0.4`, `latest` (kein Tag `0` für 0.x) |
| `v1.2.3-rc1` | `1.2.3-rc1` (kein `latest`) |

## Veröffentlichen

```bash
git tag -a v0.1.0 -m "Atrium Tablinum 0.1.0"
git push origin v0.1.0
```

Der Verlauf steht unter „Actions“ im Repo. Das fertige Paket erscheint unter „Packages“ und trägt über das Label `org.opencontainers.image.source` die Verbindung zum Repo.

## Das Image verwenden

In der Compose-Datei steht statt `build: .` die Adresse mit fester Version, z. B. `ghcr.io/<owner>/<repo>:0.1.0`. Eine feste Version ist besser als `latest`, weil ein Update dann bewusst geschieht.

Das Paket entsteht **privat**. Wer es ziehen soll, braucht Zugriff (Paket-Einstellungen, „Manage access“) und meldet sich mit einem Token an (personal access token classic mit `read:packages`):

```bash
echo "$TOKEN" | docker login ghcr.io -u <benutzer> --password-stdin
```

Ein Paket öffentlich zu machen lässt sich nicht wieder rückgängig machen. Das sollte erst geschehen, wenn Lizenz und Name feststehen.

## Hinweise

- Veröffentlicht wird nur das Server-Image. Postgres und TEI kommen weiter von ihren Herstellern.
- Das Server-Image gibt es auch für arm64. Der TEI-Server nicht: Für ARM-Hosts muss das Embedding über ein API-Backend laufen (`openai` oder `bedrock`, siehe [embedding.md](embedding.md)).
- Rückgängig machen: Ein veröffentlichtes Image bleibt unter seinem Tag bestehen. Zurück geht man mit der Compose-Datei auf die frühere Version.
