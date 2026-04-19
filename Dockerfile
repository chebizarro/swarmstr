# syntax=docker/dockerfile:1.7

# metiq — production-grade multi-stage Docker build for AI agent workloads.
#
# Two runtime variants:
#   Default (bookworm):      docker build .
#   Slim (bookworm-slim):    docker build --build-arg METIQ_VARIANT=slim .
#
# Cross-platform:
#   docker buildx build --platform linux/amd64,linux/arm64 .
#
# Build args:
#   VERSION                      — version string injected into binary (default: dev)
#   METIQ_VARIANT                — runtime base: "default" (bookworm) or "slim" (bookworm-slim)
#   METIQ_APT_PACKAGES           — extra apt packages to install at build time
#   METIQ_INSTALL_BROWSER        — set to "1" to bake in Chromium + Xvfb (~300 MB)
#   METIQ_INSTALL_DOCKER_CLI     — set to "1" to install Docker CLI for sandbox management
#   METIQ_INSTALL_PYTHON         — set to "1" to install Python 3, pip, and uv (for MCP servers)
#   METIQ_INSTALL_NODE           — set to "1" to install Node.js + npm (for JS MCP servers)
#
# Examples:
#   docker build .
#   docker build --build-arg METIQ_VARIANT=slim .
#   docker build --build-arg METIQ_INSTALL_BROWSER=1 --build-arg METIQ_INSTALL_PYTHON=1 .
#   docker build --build-arg METIQ_APT_PACKAGES="ffmpeg imagemagick" .

ARG METIQ_VARIANT=default
ARG VERSION=dev

# Base images pinned to SHA256 digests for reproducible builds.
# To update: docker buildx imagetools inspect golang:1.25-bookworm
ARG GOLANG_IMAGE="golang:1.25-bookworm@sha256:29e59af995c51a5bf63d072eca973b918e0e7af4db0e4667aa73f1b8da1a6d8c"
ARG DEBIAN_BOOKWORM_IMAGE="debian:bookworm@sha256:1d6cd964917a13b547d1ea392dff9a000c3f36070686ebc5c8755d53fb374435"
ARG DEBIAN_BOOKWORM_SLIM_IMAGE="debian:bookworm-slim@sha256:4724b8cc51e33e398f0e2e15e18d5ec2851ff0c2280647e1310bc1642182655d"
ARG NODE_IMAGE="node:24-bookworm-slim@sha256:879b21aec4a1ad820c27ccd565e7c7ed955f24b92e6694556154f251e4bdb240"

# ── Stage 1: Build ──────────────────────────────────────────────────────────────
FROM ${GOLANG_IMAGE} AS builder

ARG VERSION
# TARGETOS / TARGETARCH are set automatically by buildx.
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

# Cache module downloads before copying sources.
COPY go.mod go.sum ./
RUN --mount=type=cache,id=metiq-gomod,target=/go/pkg/mod,sharing=locked \
    go mod download

# Copy sources.
COPY . .

# Compile daemon and CLI as static binaries (no CGO required).
RUN --mount=type=cache,id=metiq-gobuild,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/metiqd \
      ./cmd/metiqd && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION}" \
      -o /out/metiq \
      ./cmd/metiq

# ── Runtime base images ─────────────────────────────────────────────────────────
FROM ${DEBIAN_BOOKWORM_IMAGE} AS base-default
LABEL org.opencontainers.image.base.name="docker.io/library/debian:bookworm"

FROM ${DEBIAN_BOOKWORM_SLIM_IMAGE} AS base-slim
LABEL org.opencontainers.image.base.name="docker.io/library/debian:bookworm-slim"

# ── Stage 2: Runtime ────────────────────────────────────────────────────────────
FROM base-${METIQ_VARIANT}
ARG METIQ_VARIANT

# OCI metadata labels.
LABEL org.opencontainers.image.source="https://github.com/metiq/metiq" \
  org.opencontainers.image.url="https://metiq.ai" \
  org.opencontainers.image.documentation="https://docs.metiq.ai/install/docker" \
  org.opencontainers.image.licenses="MIT" \
  org.opencontainers.image.title="metiq" \
  org.opencontainers.image.description="metiq — autonomous AI agent runtime container image"

WORKDIR /app

# ── Core system packages ────────────────────────────────────────────────────────
# Install baseline tools present in bookworm but missing in bookworm-slim.
# On the full image most are already installed; apt-get is a no-op for those.
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates \
      curl \
      wget \
      git \
      openssl \
      procps \
      hostname \
      jq \
      poppler-utils \
      tzdata

# ── Optional: Python 3 + uv (for MCP servers, skills, and extensions) ──────────
# Build with: docker build --build-arg METIQ_INSTALL_PYTHON=1 .
ARG METIQ_INSTALL_PYTHON=""
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    if [ -n "$METIQ_INSTALL_PYTHON" ]; then \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        python3 python3-pip python3-venv && \
      curl -LsSf https://astral.sh/uv/install.sh | sh && \
      ln -sf /root/.local/bin/uv  /usr/local/bin/uv && \
      ln -sf /root/.local/bin/uvx /usr/local/bin/uvx && \
      uv --version; \
    fi

# ── Optional: Node.js (for JavaScript/TypeScript MCP servers) ───────────────────
# Build with: docker build --build-arg METIQ_INSTALL_NODE=1 .
ARG METIQ_INSTALL_NODE=""
ARG NODE_IMAGE
RUN if [ -n "$METIQ_INSTALL_NODE" ]; then \
      curl -fsSL https://deb.nodesource.com/setup_24.x | bash - && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nodejs && \
      npm install -g npm@latest && \
      node --version && npm --version; \
    fi

# ── Optional: Extra apt packages ────────────────────────────────────────────────
# Build with: docker build --build-arg METIQ_APT_PACKAGES="ffmpeg imagemagick" .
ARG METIQ_APT_PACKAGES=""
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    if [ -n "$METIQ_APT_PACKAGES" ]; then \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $METIQ_APT_PACKAGES; \
    fi

# ── Optional: Chromium + Xvfb for browser automation ────────────────────────────
# Build with: docker build --build-arg METIQ_INSTALL_BROWSER=1 .
# Adds ~300 MB but eliminates Playwright install on every container start.
ARG METIQ_INSTALL_BROWSER=""
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    if [ -n "$METIQ_INSTALL_BROWSER" ]; then \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        chromium \
        xvfb \
        fonts-liberation \
        libnss3 \
        libatk-bridge2.0-0 \
        libdrm2 \
        libxcomposite1 \
        libxdamage1 \
        libxrandr2 \
        libgbm1 \
        libasound2 \
        libpango-1.0-0 \
        libcairo2 && \
      echo "Chromium $(chromium --version 2>/dev/null || echo 'installed')"; \
    fi

# ── Optional: Docker CLI for sandbox container management ───────────────────────
# Build with: docker build --build-arg METIQ_INSTALL_DOCKER_CLI=1 .
# Adds ~50 MB. Only the CLI is installed — no Docker daemon.
ARG METIQ_INSTALL_DOCKER_CLI=""
ARG METIQ_DOCKER_GPG_FINGERPRINT="9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    if [ -n "$METIQ_INSTALL_DOCKER_CLI" ]; then \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates curl gnupg && \
      install -m 0755 -d /etc/apt/keyrings && \
      curl -fsSL https://download.docker.com/linux/debian/gpg -o /tmp/docker.gpg.asc && \
      expected_fingerprint="$(printf '%s' "$METIQ_DOCKER_GPG_FINGERPRINT" | tr '[:lower:]' '[:upper:]' | tr -d '[:space:]')" && \
      actual_fingerprint="$(gpg --batch --show-keys --with-colons /tmp/docker.gpg.asc | awk -F: '$1 == "fpr" { print toupper($10); exit }')" && \
      if [ -z "$actual_fingerprint" ] || [ "$actual_fingerprint" != "$expected_fingerprint" ]; then \
        echo "ERROR: Docker apt key fingerprint mismatch (expected $expected_fingerprint, got ${actual_fingerprint:-<empty>})" >&2; \
        exit 1; \
      fi && \
      gpg --dearmor -o /etc/apt/keyrings/docker.gpg /tmp/docker.gpg.asc && \
      rm -f /tmp/docker.gpg.asc && \
      chmod a+r /etc/apt/keyrings/docker.gpg && \
      printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable\n' \
        "$(dpkg --print-architecture)" > /etc/apt/sources.list.d/docker.list && \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        docker-ce-cli docker-compose-plugin; \
    fi

# ── Copy binaries and skills ────────────────────────────────────────────────────
COPY --from=builder /out/metiqd /usr/local/bin/metiqd
COPY --from=builder /out/metiq  /usr/local/bin/metiq
COPY skills/ /app/skills/

# Ensure skills directory permissions are sane.
RUN find /app/skills -type d -exec chmod 755 {} + && \
    find /app/skills -type f -exec chmod 644 {} +

# Expose CLI on PATH without requiring global installs.
RUN ln -sf /usr/local/bin/metiq /usr/local/bin/mq

ENV METIQ_BUNDLED_SKILLS_DIR=/app/skills

# ── Non-root user ───────────────────────────────────────────────────────────────
# Security hardening: run as non-root to reduce container escape attack surface.
RUN groupadd -g 1000 metiq && \
    useradd -m -u 1000 -g metiq -d /home/metiq -s /bin/bash metiq && \
    mkdir -p /data && chown metiq:metiq /data && \
    chown -R metiq:metiq /app

ENV HOME=/data
VOLUME ["/data"]

USER metiq

# ── Health check ────────────────────────────────────────────────────────────────
# Admin API health endpoint (enabled via --admin-addr or admin_listen_addr).
# Falls back to checking whether the process is alive if admin API is disabled.
HEALTHCHECK --interval=60s --timeout=10s --start-period=15s --retries=3 \
  CMD curl -fsS http://127.0.0.1:7423/health || kill -0 1

# Admin API (optional; enabled via --admin-addr or admin_listen_addr in config).
EXPOSE 7423

ENTRYPOINT ["/usr/local/bin/metiqd"]
