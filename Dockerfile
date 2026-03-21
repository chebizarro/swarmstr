# metiq — multi-stage Docker build.
#
# Two runtime variants:
#   Default (alpine):  docker build .
#   Slim (scratch):    docker build --build-arg METIQ_VARIANT=slim .
#
# Cross-platform:
#   docker buildx build --platform linux/amd64,linux/arm64 .
#
# Build args:
#   VERSION          — version string injected into binaries (default: dev)
#   METIQ_VARIANT — runtime base: "default" (alpine) or "slim" (scratch)

ARG METIQ_VARIANT=default

# ── Stage: CA certificates (shared between variants) ───────────────────────────
FROM alpine:3.21 AS certs
RUN apk add --no-cache ca-certificates tzdata

# ── Stage: Build ───────────────────────────────────────────────────────────────
FROM golang:1.25-alpine AS builder

ARG VERSION=dev
# TARGETOS / TARGETARCH are set automatically by buildx.
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

# Cache module downloads before copying sources.
COPY go.mod go.sum ./
RUN go mod download

# Copy sources.
COPY . .

# Compile daemon and CLI as static binaries (no CGO required).
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/metiqd \
      ./cmd/metiqd && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/metiq \
      ./cmd/metiq

# ── Runtime: default (alpine) ──────────────────────────────────────────────────
FROM alpine:3.21 AS runtime-default

# ca-certificates: outbound TLS; tzdata: correct timestamps; poppler-utils: pdftotext
RUN apk add --no-cache ca-certificates tzdata poppler-utils

WORKDIR /app

COPY --from=builder /out/metiqd /usr/local/bin/metiqd
COPY --from=builder /out/metiq  /usr/local/bin/metiq
COPY skills/ /app/skills/

ENV METIQ_BUNDLED_SKILLS_DIR=/app/skills \
    HOME=/data

VOLUME ["/data"]

# Admin API (optional; enabled via --admin-addr or admin_listen_addr in config).
EXPOSE 7423

ENTRYPOINT ["/usr/local/bin/metiqd"]

# ── Runtime: slim (scratch + certs only) ──────────────────────────────────────
FROM scratch AS runtime-slim

# Bring in CA bundle and timezone data from the certs stage.
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=certs /usr/share/zoneinfo                 /usr/share/zoneinfo

COPY --from=builder /out/metiqd /usr/local/bin/metiqd
COPY --from=builder /out/metiq  /usr/local/bin/metiq
COPY skills/ /app/skills/

ENV METIQ_BUNDLED_SKILLS_DIR=/app/skills \
    HOME=/data

VOLUME ["/data"]
EXPOSE 7423

ENTRYPOINT ["/usr/local/bin/metiqd"]

# ── Final target (selected by METIQ_VARIANT) ────────────────────────────────
# shellcheck disable=SC2154  (ARG is used via FROM)
FROM runtime-${METIQ_VARIANT} AS final
