# syntax=docker/dockerfile:1.7

# metiq daemon — compose-friendly build.
#
# This Dockerfile mirrors the root Dockerfile but is tailored for
# docker-compose use (context set to repo root via compose.yml).
#
# Build args are forwarded from docker-compose.yml — see .env.example.

ARG METIQ_VARIANT=default
ARG VERSION=dev

ARG GOLANG_IMAGE="golang:1.25-bookworm"
ARG DEBIAN_BOOKWORM_IMAGE="debian:bookworm"
ARG DEBIAN_BOOKWORM_SLIM_IMAGE="debian:bookworm-slim"

# ── Stage 1: Build ──────────────────────────────────────────────────────────────
FROM ${GOLANG_IMAGE} AS builder

ARG VERSION
ARG COMMIT
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,id=metiq-gomod,target=/go/pkg/mod,sharing=locked \
    go mod download

COPY . .

RUN --mount=type=cache,id=metiq-gobuild,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/metiqd \
      ./cmd/metiqd && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/metiq \
      ./cmd/metiq

# ── Runtime base images ─────────────────────────────────────────────────────────
FROM ${DEBIAN_BOOKWORM_IMAGE} AS base-default
FROM ${DEBIAN_BOOKWORM_SLIM_IMAGE} AS base-slim

# ── Stage 2: Runtime ────────────────────────────────────────────────────────────
FROM base-${METIQ_VARIANT}

LABEL org.opencontainers.image.title="metiq" \
  org.opencontainers.image.description="metiq AI agent runtime (compose build)"

WORKDIR /app

# Core system packages.
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      ca-certificates curl wget git openssl procps hostname jq poppler-utils tzdata su-exec

# Optional: Python 3 + uv.
ARG METIQ_INSTALL_PYTHON=""
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    if [ -n "$METIQ_INSTALL_PYTHON" ]; then \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        python3 python3-pip python3-venv && \
      curl -LsSf https://astral.sh/uv/install.sh | sh && \
      ln -sf /root/.local/bin/uv  /usr/local/bin/uv && \
      ln -sf /root/.local/bin/uvx /usr/local/bin/uvx; \
    fi

# Optional: Node.js.
ARG METIQ_INSTALL_NODE=""
RUN if [ -n "$METIQ_INSTALL_NODE" ]; then \
      curl -fsSL https://deb.nodesource.com/setup_24.x | bash - && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends nodejs; \
    fi

# Optional: Extra apt packages.
ARG METIQ_APT_PACKAGES=""
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    if [ -n "$METIQ_APT_PACKAGES" ]; then \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $METIQ_APT_PACKAGES; \
    fi

# Optional: Browser automation.
ARG METIQ_INSTALL_BROWSER=""
RUN --mount=type=cache,id=metiq-apt-cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,id=metiq-apt-lists,target=/var/lib/apt/lists,sharing=locked \
    if [ -n "$METIQ_INSTALL_BROWSER" ]; then \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        chromium xvfb fonts-liberation libnss3 libatk-bridge2.0-0 \
        libdrm2 libxcomposite1 libxdamage1 libxrandr2 libgbm1 \
        libasound2 libpango-1.0-0 libcairo2; \
    fi

# Optional: Docker CLI.
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
      expected_fp="$(printf '%s' "$METIQ_DOCKER_GPG_FINGERPRINT" | tr '[:lower:]' '[:upper:]' | tr -d '[:space:]')" && \
      actual_fp="$(gpg --batch --show-keys --with-colons /tmp/docker.gpg.asc | awk -F: '$1 == "fpr" { print toupper($10); exit }')" && \
      if [ -z "$actual_fp" ] || [ "$actual_fp" != "$expected_fp" ]; then \
        echo "ERROR: Docker GPG fingerprint mismatch" >&2; exit 1; \
      fi && \
      gpg --dearmor -o /etc/apt/keyrings/docker.gpg /tmp/docker.gpg.asc && \
      rm -f /tmp/docker.gpg.asc && chmod a+r /etc/apt/keyrings/docker.gpg && \
      printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian bookworm stable\n' \
        "$(dpkg --print-architecture)" > /etc/apt/sources.list.d/docker.list && \
      apt-get update && \
      DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        docker-ce-cli docker-compose-plugin; \
    fi

# Copy binaries and skills.
COPY --from=builder /out/metiqd /usr/local/bin/metiqd
COPY --from=builder /out/metiq  /usr/local/bin/metiq
COPY skills/ /app/skills/
COPY scripts/docker/metiqd-entrypoint.sh /entrypoint.sh

RUN find /app/skills -type d -exec chmod 755 {} + && \
    find /app/skills -type f -exec chmod 644 {} +

RUN chmod +x /entrypoint.sh

RUN ln -sf /usr/local/bin/metiq /usr/local/bin/mq

ENV METIQ_BUNDLED_SKILLS_DIR=/app/skills

# Non-root user.
RUN groupadd -g 1000 metiq && \
    useradd -m -u 1000 -g metiq -d /home/metiq -s /bin/bash metiq && \
    mkdir -p /data && chown metiq:metiq /data && \
    chown -R metiq:metiq /app

ENV HOME=/data
VOLUME ["/data"]

# Run entrypoint as root to fix permissions, then drop to metiq user
# USER metiq

HEALTHCHECK --interval=60s --timeout=10s --start-period=15s --retries=3 \
  CMD curl -fsS http://127.0.0.1:7423/health || kill -0 1

EXPOSE 7423

ENTRYPOINT ["/entrypoint.sh"]
