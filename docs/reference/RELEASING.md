---
title: "Release Checklist"
summary: "Step-by-step release checklist for metiq Go binary + Docker releases"
read_when:
  - Cutting a new metiq release
  - Verifying CI artifacts before publishing
  - Tagging a release
---

# Release Checklist

metiq releases are driven by Git tags. Pushing a `v*` tag triggers the
[Docker Release workflow](/.github/workflows/docker-release.yml), which:

1. Builds and pushes multi-arch Docker images (`linux/amd64`, `linux/arm64`) to GHCR.
2. Builds and uploads standalone binaries for `linux`, `darwin`, and `windows` (amd64 + arm64)
   to the GitHub release as assets.

## Release Steps

### 1. Pre-flight

- [ ] All tests pass: `go test ./...`
- [ ] Build clean: `go build ./...`
- [ ] Working tree is clean: `git status`
- [ ] `main` branch is up to date with remote

### 2. Bump version

metiq uses date-based version tags (`YYYY.MM.DD` or `YYYY.MM.DD-N` for same-day patches):

```bash
# Example
git tag v2026.03.10
```

For patch releases on the same day:

```bash
git tag v2026.03.10-1
```

### 3. Tag and push

```bash
git push origin v2026.03.10
```

This triggers:
- `docker-release.yml` — builds `ghcr.io/<org>/metiq:<version>` and `ghcr.io/<org>/metiq:latest`
- Also builds `metiqd` and `metiq` binaries for all platforms and attaches them to the release

### 4. Verify release

- [ ] GitHub Actions workflow completes successfully
- [ ] Docker images are available on GHCR:
  - `ghcr.io/<org>/metiq:<version>`
  - `ghcr.io/<org>/metiq:latest`
  - `ghcr.io/<org>/metiq:<version>-slim`
  - `ghcr.io/<org>/metiq:latest-slim`
- [ ] GitHub release has binary assets for all platforms:
  - `metiqd-linux-amd64`
  - `metiqd-linux-arm64`
  - `metiqd-darwin-amd64`
  - `metiqd-darwin-arm64`
  - `metiqd-windows-amd64.exe`
  - `metiq-linux-amd64` (CLI)
  - `metiq-darwin-arm64` (CLI, etc.)
- [ ] Pull and smoke test the image: `docker run --rm ghcr.io/<org>/metiq:<version> metiqd --version`
- [ ] (Optional) Announce release notes

## Docker Variants

| Tag suffix | Description |
|------------|-------------|
| _(none)_ | Default — includes all tools |
| `-slim` | Minimal image, smaller footprint |

Both variants are built for `linux/amd64` and `linux/arm64`.

## Building Locally

To build binaries locally (matching CI flags):

```bash
# Daemon
go build -trimpath -ldflags="-s -w -X main.version=v2026.03.10 -X main.commit=$(git rev-parse --short HEAD)" -o metiqd ./cmd/metiqd

# CLI
go build -trimpath -ldflags="-s -w -X main.version=v2026.03.10 -X main.commit=$(git rev-parse --short HEAD)" -o metiq ./cmd/metiq

# Cross-compile (e.g. Linux arm64 from macOS)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o metiqd-linux-arm64 ./cmd/metiqd
```

## See Also

- [CI workflow](/.github/workflows/ci.yml)
- [Docker Release workflow](/.github/workflows/docker-release.yml)
- [Install](/install/)
- [Docker](/install/docker)
