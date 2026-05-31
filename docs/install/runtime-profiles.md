---
summary: "Supported Docker and bare-metal runtime profiles"
read_when:
  - Choosing a Docker image profile
  - Installing metiq under systemd
  - Comparing Docker and bare-metal deployments
title: "Runtime Profiles"
---

# Runtime Profiles

metiq supports the same daemon and agent behavior in Docker and on bare metal. Profiles only change packaging and supervision: which optional OS tools are preinstalled, where state is written, and whether Docker or systemd restarts the process.

## Docker profiles

The Dockerfiles expose one base runtime with optional build layers. Approximate sizes are uncompressed local `docker images` sizes for linux/amd64 and vary by Debian package updates, architecture, and bundled skills.

| Profile | Build settings | Includes | Approx. image size |
| --- | --- | --- | --- |
| `minimal` | no optional `METIQ_INSTALL_*` args | Static `metiqd`/`metiq`, bundled skills, Debian runtime packages, `task`, `curl`, `git`, `jq`, `poppler-utils` | ~450-650 MB |
| `full` | `METIQ_INSTALL_PYTHON=1`, `METIQ_INSTALL_NODE=1`, optional `METIQ_INSTALL_DOCKER_CLI=1` | Minimal plus Python 3, pip, uv/uvx, Node.js/npm, and optionally Docker CLI/Compose plugin | ~650-900 MB |
| `browser-enabled` | `METIQ_INSTALL_PYTHON=1`, `METIQ_INSTALL_NODE=1`, `METIQ_INSTALL_BROWSER=1`, optional `METIQ_INSTALL_DOCKER_CLI=1` | Full agent runtime plus Chromium and Xvfb for in-container browser automation | ~950 MB-1.2 GB |

Optional layer deltas are roughly: Chromium+Xvfb `+~300 MB`, Docker CLI `+~50 MB`, and Python/Node depend on current Debian and upstream package versions.

### Compose mapping

- `docker compose up -d` builds the `minimal` profile unless optional `METIQ_INSTALL_*` args are set in `.env`.
- `docker compose --profile full up -d` builds `metiqd-full` with Python, Node.js, and Chromium baked in; this compose service name predates the profile terminology and corresponds to the baked `browser-enabled` image shape.
- `docker compose --profile browser up -d` starts the standalone Playwright/browser sandbox sidecar instead of changing daemon semantics.

The container healthcheck first checks `http://127.0.0.1:7423/health` when the admin API is enabled. If the admin API is disabled, it falls back to `kill -0 1`, matching the Dockerfile healthcheck and treating the daemon process as healthy while it is alive.

## Bare-metal/systemd profile

`scripts/systemd/metiqd.service` is a user service with these assumptions:

- Binary: `%h/.local/bin/metiqd`.
- Environment file: optional `%h/.metiq/.env`.
- Persistent writable state: `%h/.metiq` and `%h/.local/share/metiq`.
- Temporary files: service-private `/tmp` because `PrivateTmp=yes` is enabled.
- Filesystem sandbox: `ProtectSystem=strict`; only `ReadWritePaths` are writable.
- Optional dependencies: Python+uv, Node.js, Chromium/Xvfb, Docker CLI, and other tool-specific binaries must be installed on the host if enabled tools/plugins require them.
- Supervision: systemd restarts the daemon with `Restart=on-failure`; Docker deployments use `restart: unless-stopped`.

Docker image layering affects footprint and container startup time. The systemd profile instead depends on host packages and changes filesystem/sandbox behavior. The agent runtime, Nostr event handling, tool semantics, state files, and feature behavior should not diverge between Docker and bare-metal deployments beyond those packaging and supervision differences.

## Restart recovery model

Docker `restart: unless-stopped` and systemd `Restart=on-failure` are supervisors for the same daemon recovery model: restart the process, reload durable state, report what was recoverable or lost, and do not blindly replay in-flight mutating work.

On startup, `metiqd` exposes recovery details in `/health` and `status.get`: DM ingest checkpoint state, explicit turn outcome mode (`lost_but_visible` by default), ACP tasks marked lost due to restart, and SQLite integrity/backup/rebuild outcomes. ACP tasks that were queued or running are marked terminal `lost` with a restart reason. In-memory background agent jobs and subagent registries are reset; unfinished runs from the prior process are considered lost and operator-visible in recovery status.

Turn checkpoints are not an automatic replay mechanism. They may only be used by explicitly resume-safe, read-only cases; mutating tool calls are not resumed after restart. Operators should treat restart as a visibility boundary: completed durable work remains available, but in-flight side-effectful work must be reviewed and intentionally reissued if still desired.
