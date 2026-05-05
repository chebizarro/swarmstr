# syntax=docker/dockerfile:1.7

FROM golang:1.25-bookworm AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=cleanup-smoke-gomod,target=/go/pkg/mod,sharing=locked \
    go mod download
COPY . .
RUN --mount=type=cache,id=cleanup-smoke-gobuild,target=/root/.cache/go-build,sharing=locked \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X main.version=dev" \
      -o /out/metiq \
      ./cmd/metiq && \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
      -trimpath \
      -ldflags="-s -w -X main.version=dev" \
      -o /out/metiqd \
      ./cmd/metiqd

FROM debian:bookworm-slim

ARG METIQ_TASK_VERSION=v3.50.0

RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash \
    ca-certificates \
    curl \
    git \
    procps \
  && sh -c "$(curl --location https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin ${METIQ_TASK_VERSION} \
  && task --version \
  && rm -rf /var/lib/apt/lists/*

WORKDIR /repo
COPY --from=builder /out/metiq /usr/local/bin/metiq
COPY --from=builder /out/metiqd /usr/local/bin/metiqd
COPY . .
COPY scripts/docker/cleanup-smoke/run.sh /usr/local/bin/metiq-cleanup-smoke
RUN chmod +x /usr/local/bin/metiq-cleanup-smoke

ENTRYPOINT ["/usr/local/bin/metiq-cleanup-smoke"]
