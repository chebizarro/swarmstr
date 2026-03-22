# Migration from swarmstr to metiq

The project binaries and Go module have been renamed from `swarmstr` / `swarmstrd` to `metiq` / `metiqd`. The state directory moves from `~/.swarmstr` to `~/.metiq`.

## Automated migration

An automated migration script handles everything:

```sh
# Dry run (preview changes, nothing modified):
bash scripts/migrate-from-swarmstr.sh

# Apply changes:
bash scripts/migrate-from-swarmstr.sh --apply
```

The script is idempotent — safe to run multiple times. It preserves `~/.swarmstr` as a backup.

## What the script does

### Step 1: State directory
Copies `~/.swarmstr` → `~/.metiq`. This includes:
- `bootstrap.json` — daemon startup config (private key, relays, model)
- `config.json` / `config.yaml` — live config (hot-reloaded)
- `.env` — secrets / environment variables
- `workspace/` — agent workspace, hooks, skills
- `tasks.json` — persistent task queue (if present)
- `plugins/` — installed plugins
- `skills/` — managed skills
- `metiqd.pid` / `metiqd.log` — daemon PID and log files (if present)

The original `~/.swarmstr` is left intact as a backup.

### Step 2: Environment variables
Renames `SWARMSTR_*` → `METIQ_*` inside `~/.metiq/.env`. Known variables:

| Old | New | Purpose |
|-----|-----|---------|
| `SWARMSTR_PRIVATE_KEY` | `METIQ_PRIVATE_KEY` | Nostr identity private key |
| `SWARMSTR_HOME` | `METIQ_HOME` | Override state directory location |
| `SWARMSTR_ADMIN_ADDR` | `METIQ_ADMIN_ADDR` | Admin API listen address |
| `SWARMSTR_ADMIN_TOKEN` | `METIQ_ADMIN_TOKEN` | Admin API bearer token |
| `SWARMSTR_BUNDLED_SKILLS_DIR` | `METIQ_BUNDLED_SKILLS_DIR` | Bundled skills path (Docker) |

### Step 3: Config files
Updates references to `swarmstr` / `swarmstrd` / `SWARMSTR_*` inside `bootstrap.json`, `config.json`, and `config.yaml` (if present).

### Step 4: Systemd service
If `~/.config/systemd/user/swarmstrd.service` exists, creates `metiqd.service` with all paths and binary names updated. Prints switchover instructions:
```sh
systemctl --user disable swarmstrd
systemctl --user enable --now metiqd
```

### Step 5: Binary check
Verifies `metiqd` and `metiq` are on `PATH`. If only the old binaries (`swarmstrd` / `swarmstr`) are found, prints suggested symlink commands. No changes are made automatically — you'll need to create symlinks or install the new binaries yourself.

### Step 6: Shell environment
Scans the current shell for any active `SWARMSTR_*` environment variables and prints the required renames. You'll need to update these manually in your shell profile (`~/.bashrc`, `~/.zshrc`, etc.):
```sh
# Before:
export SWARMSTR_PRIVATE_KEY="nsec1..."
# After:
export METIQ_PRIVATE_KEY="nsec1..."
```

## Manual migration (if not using the script)

1. **Copy state directory:**
   ```sh
   cp -a ~/.swarmstr ~/.metiq
   ```

2. **Rename env vars in `~/.metiq/.env`:**
   ```sh
   # GNU sed (Linux):
   sed -i 's/SWARMSTR_/METIQ_/g' ~/.metiq/.env
   # BSD sed (macOS):
   sed -i '' 's/SWARMSTR_/METIQ_/g' ~/.metiq/.env
   ```

3. **Update config files** — replace `swarmstrd` → `metiqd`, `swarmstr` → `metiq`, `SWARMSTR_` → `METIQ_` in `bootstrap.json` and `config.json`.

4. **Update shell profile** — rename any `SWARMSTR_*` exports to `METIQ_*`.

5. **Update systemd** (if applicable):
   ```sh
   cp ~/.config/systemd/user/swarmstrd.service ~/.config/systemd/user/metiqd.service
   # Edit the new file to update all paths and binary names
   systemctl --user daemon-reload
   systemctl --user disable swarmstrd
   systemctl --user enable --now metiqd
   ```

6. **Update automation** — any scripts, cron jobs, or CI/CD referencing old binary names or paths.

## Post-migration cleanup

Once you've confirmed metiq is running correctly:
```sh
rm -rf ~/.swarmstr
systemctl --user disable swarmstrd 2>/dev/null  # if applicable
```

## Notes
- Historical references in older docs and beads issues may still mention `swarmstr`; these are historical only, not compatibility shims.
- The Go module path is `metiq` — no `swarmstr` import paths exist in the codebase.
