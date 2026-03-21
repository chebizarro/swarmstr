# Migration to metiq

The project binaries and Go module have been renamed from `swarmstr` / `swarmstrd` to `metiq` / `metiqd`.

Migration steps:
- Rename binaries: `swarmstr` -> `metiq`, `swarmstrd` -> `metiqd`.
- Move state/config directory: `~/.swarmstr` -> `~/.metiq`.
- Rename environment variables: `SWARMSTR_*` -> `METIQ_*`.
- Update any systemd units, scripts, or automation referencing old binary names or paths.

Notes:
- Historical references in older docs and fixtures may still mention `swarmstr`; they are not runtime compatibility shims.
- External registry/repository settings outside this repo should be updated alongside release and deployment infrastructure.
