# ── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS builder

WORKDIR /src

# Cache module downloads
COPY go.mod go.sum ./
RUN go mod download

# Copy sources
COPY . .

# Build the daemon (static binary — no CGO needed)
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/metiqd \
      ./cmd/metiqd

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.21

# ca-certificates for TLS, tzdata for timestamps
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy the binary
COPY --from=builder /out/metiqd /usr/local/bin/metiqd
COPY --from=builder /out/metiqd /usr/local/bin/swarmstrd

# Copy bundled skills (SKILL.md format)
COPY skills/ /app/skills/

# Environment defaults — override at runtime via -e or --env-file
ENV SWARMSTR_BUNDLED_SKILLS_DIR=/app/skills

# Config dir lives in a volume at /data
ENV HOME=/data
VOLUME ["/data"]

# metiqd listens on no fixed port (Nostr relay WS is outbound),
# but expose 8080 in case a local HTTP/RPC listener is added later.
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/metiqd"]
