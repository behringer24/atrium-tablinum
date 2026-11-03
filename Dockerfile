# Gebaut wird auf der Plattform des Builders und für die Zielplattform kreuzkompiliert, damit
# arm64-Images ohne Emulation des Go-Compilers entstehen (docker buildx --platform ...).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
# Release-Version für server_info; ohne Angabe meldet das Binary "dev".
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Migrationen sind per go:embed im Binary, das Image braucht keine weiteren Dateien.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w -X github.com/behringer24/atrium-tablinum/internal/product.Version=${VERSION}" -o /out/atrium-tablinum ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 atrium
USER atrium
COPY --from=build /out/atrium-tablinum /usr/local/bin/atrium-tablinum
COPY LICENSE NOTICE /licenses/
LABEL org.opencontainers.image.title="Atrium Tablinum" \
      org.opencontainers.image.description="Shared knowledge store for AI assistants (MCP)" \
      org.opencontainers.image.licenses="Elastic-2.0" \
      org.opencontainers.image.source="https://github.com/behringer24/atrium-tablinum" \
      org.opencontainers.image.vendor="behringer24"
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=20s --retries=5 \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["atrium-tablinum"]
CMD ["-config", "/etc/atrium-tablinum/config.yaml"]
