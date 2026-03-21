# Migration to metiq

The project binaries and Go module have been renamed from `swarmstr` / `swarmstrd` to `metiq` / `metiqd`.

Compatibility preserved in this migration:
- Legacy `SWARMSTR_*` environment variables remain supported.
- Legacy state/config locations under `~/.swarmstr` remain supported.
- CLI daemon management accepts either `metiqd` or legacy `swarmstrd` on `PATH`.
- Docker images and installer flows can provide `swarmstr` / `swarmstrd` aliases where configured.

Intentionally not renamed yet:
- Historical references in older docs and fixtures.
- Existing `~/.swarmstr` paths and `SWARMSTR_*` prefixes, to avoid breaking upgrades.
- External registry/repository settings outside this repo, which should be updated alongside release and deployment infrastructure.
