# The build runs on the builder's platform and cross-compiles for the target platform, so that
# arm64 images are produced without emulating the Go compiler (docker buildx --platform ...).
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
# Release version for server_info; without a value the binary reports "dev".
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Migrations are in the binary via go:embed, the image needs no further files.
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
