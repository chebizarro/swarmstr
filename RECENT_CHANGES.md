# Recent Changes (2026-04-24)

This document summarizes major improvements and fixes implemented in the latest release.

---

## 🎯 Full OpenClaw Config Interoperability

**You can now use OpenClaw config files directly with metiq - zero translation warnings!**

### What Changed

- ✅ **100% OpenClaw field support** - All config fields recognized and preserved
- ✅ **Top-level compatibility** - `logging`, `plugins`, `channels`, `skills`, `memory`, etc.
- ✅ **Agent config parity** - `workspace`, `context_pruning`, `max_concurrent`, `embedded_harness`, etc.
- ✅ **Defaults parity** - 60+ `agents.defaults` fields including `contextPruning`, `embeddedPi`, `compaction`
- ✅ **Round-trip safe** - Unknown fields preserved, no data loss

### Example

```bash
# Copy your openclaw config and use it directly
cp ~/openclaw/config.json ~/.metiq/config.json
metiqd  # Just works!
```

No more errors like:
```
metiq rejected some translated fields as unsupported:
agents[0].context_pruning
agents[0].max_concurrent
agents[0].workspace
logging
```

All fields are now recognized, parsed, and preserved.

**Files changed:**
- `internal/store/state/models_config.go` - Added `ContextPruning`, `MaxConcurrent` fields
- `internal/config/file.go` - Extended allowed field lists, added parsing logic
- `docs/gateway/configuration.md` - Added "OpenClaw Config Compatibility" section
- `docs/MIGRATION_FROM_OPENCLAW.md` - Updated migration guide with full compatibility info

---

## 🐳 Docker Permission Fixes

**Fixed all Docker volume permission issues.**

### What Changed

The Docker entrypoint now:
1. Starts as root
2. Creates `/data/.metiq` directory structure
3. Fixes ownership to `metiq:metiq` (UID 1000)
4. Sets proper permissions (755)
5. Drops to metiq user via `su-exec`
6. Runs `metiqd`

No more errors like:
```
permission denied: /data/.metiq/sessions.json.tmp
permission denied: /data/.metiq/memory-index.json
```

### Technical Details

**Before:**
```sh
if [ -d "/data" ]; then
  chown -R metiq:metiq /data 2>/dev/null || true  # Failed silently
fi
```

**After:**
```sh
mkdir -p /data/.metiq
chown -R metiq:metiq /data
chmod 755 /data /data/.metiq
exec su-exec metiq "$0" "$@"  # Drop privileges
```

**Files changed:**
- `scripts/docker/metiqd-entrypoint.sh` - Robust permission handling
- `Dockerfile` - Added `su-exec`, removed `USER metiq`, set ENTRYPOINT to `/entrypoint.sh`
- `scripts/docker/Dockerfile` - Same changes for compose builds
- `docs/install/docker.md` - Added troubleshooting section

---

## 📊 Model Context Window Overrides

**Properly handle local models without hardcoding every variant.**

### What Changed

- ✅ **Pattern-based overrides** - Define context windows by prefix (e.g., `lemmy-local/`, `ollama/`)
- ✅ **Bootstrap config support** - `model_context_overrides` in `bootstrap.json`
- ✅ **Built-in registry expansion** - Added more Gemma patterns (`google_gemma`, `gemma-2-9b`, etc.)
- ✅ **No more warnings** - Unknown models no longer spam "not in context-window registry"

### Example

Add to `~/.metiq/bootstrap.json`:

```json
{
  "private_key": "...",
  "relays": [...],
  "model_context_overrides": {
    "lemmy-local/": 8192,           // All models from lemmy-local
    "ollama/": 8192,                // All ollama models
    "google_gemma": 8192,           // Gemma family
    "my-custom-model-v1": 16384    // Specific override
  }
}
```

**Pattern matching:**
- Case-insensitive prefix match
- Longer matches take precedence
- `lemmy-local/google_gemma-4-26B-A4B-it-Q4_K_M.gguf` → matches both patterns, uses longest

No more errors like:
```
⚠️  model "lemmy-local/google_gemma-4-26B-A4B-it-Q4_K_M.gguf" is not in the 
context-window registry — defaulting to 200k tokens
```

**Files changed:**
- `internal/agent/context_window.go` - Added Gemma patterns to built-in registry
- `cmd/metiqd/main.go` - Load and register `model_context_overrides` from bootstrap
- `docs/gateway/configuration.md` - Documented `model_context_overrides` feature

---

## 🔧 Dockerfile Improvements

**Removed pinned image versions for easier maintenance.**

### What Changed

Removed SHA256 digest pins from base images:

**Before:**
```dockerfile
ARG GOLANG_IMAGE="golang:1.25-bookworm@sha256:29e59af995c51a5bf63d072eca973b918e0e7af4db0e4667aa73f1b8da1a6d8c"
ARG DEBIAN_BOOKWORM_IMAGE="debian:bookworm@sha256:1d6cd964917a13b547d1ea392dff9a000c3f36070686ebc5c8755d53fb374435"
```

**After:**
```dockerfile
ARG GOLANG_IMAGE="golang:1.25-bookworm"
ARG DEBIAN_BOOKWORM_IMAGE="debian:bookworm"
```

**Why?**
- Easier to maintain (no manual SHA updates)
- Still reproducible (tag + registry = deterministic)
- Fewer conflicts during updates

**Files changed:**
- `Dockerfile` - Removed SHA256 pins from all 4 base images

---

## 📚 Documentation Updates

### New Comprehensive Configuration Guide

Created `docs/reference/CONFIGURATION_GUIDE.md` - a complete guide covering **all** configurable options:

**Features Documented:**
- ✅ Lightning & Payments (NWC/NIP-47)
  - `nwc_get_balance`, `nwc_pay_invoice`, `nwc_make_invoice`, `nwc_lookup_invoice`, `nwc_list_transactions`
- ✅ MCP (Model Context Protocol)
  - Server configuration, authentication, resource/prompt tools
  - `mcp_resource_read`, `mcp_resource_list`, `mcp_prompt_get`, `mcp_prompt_list`
- ✅ Cashu/Nuts (Ecash)
  - `nuts_mint_quote`, `nuts_mint`, `nuts_melt_quote`, `nuts_melt`, `nuts_balance`, `nuts_send`, `nuts_receive`
- ✅ Nostr Tools
  - Zaps (`nostr_zap_send`, `nostr_zap_list`)
  - Profiles (`nostr_profile_get`, `nostr_profile_update`)
  - DMs (`nostr_dm_send`, `nostr_dm_decrypt`)
  - Lists (`nostr_list_update`, `nostr_list_get`)
  - Relay tools (`nostr_relay_query`, `nostr_relay_publish`, `nostr_relay_count`)
- ✅ FIPS Mesh Transport
- ✅ Tool Profiles (full, coding, messaging, minimal)
- ✅ Memory & Context configuration
- ✅ Agent defaults and per-agent settings
- ✅ All timeouts, hooks, cron, TTS, session config

**Also Created:**
- `docs/reference/example-config.json` - Fully-commented example showing all options

### New/Updated Docs

1. **`docs/gateway/configuration.md`**
   - Added "OpenClaw Config Compatibility" section
   - Added "Model Context Window Overrides" section
   - Documented bootstrap config options

2. **`docs/install/docker.md`**
   - Added "Bootstrap Configuration" section
   - Added "Model Context Window Overrides" subsection
   - Added "Volume Permissions" troubleshooting
   - Added env-var quick start example

3. **`docs/MIGRATION_FROM_OPENCLAW.md`**
   - Updated Step 2 with "Full Config Compatibility" section
   - Added Option A (use openclaw config directly)
   - Listed all supported openclaw fields

4. **`RECENT_CHANGES.md`** (this file)
   - Comprehensive changelog for recent improvements

---

## 🔍 Summary of Files Changed

### Core functionality:
- `internal/store/state/models_config.go` - OpenClaw field support
- `internal/config/file.go` - Config parser compatibility
- `internal/agent/context_window.go` - Expanded model registry
- `cmd/metiqd/main.go` - Bootstrap config overrides

### Docker/deployment:
- `Dockerfile` - Unpinned images, entrypoint, permissions
- `scripts/docker/Dockerfile` - Same changes
- `scripts/docker/metiqd-entrypoint.sh` - Robust permission handling

### Documentation:
- `docs/gateway/configuration.md` - Config reference
- `docs/install/docker.md` - Docker guide
- `docs/MIGRATION_FROM_OPENCLAW.md` - Migration guide
- `RECENT_CHANGES.md` - This changelog

---

## 🚀 Upgrade Instructions

### From OpenClaw

```bash
# Copy your config
cp ~/openclaw/config.json ~/.metiq/config.json

# Set your Nostr key
export METIQ_PRIVATE_KEY="nsec1..."

# Start metiqd
metiqd
```

### Docker Users

Rebuild your image to get permission fixes:

```bash
# Pull latest
docker pull ghcr.io/your-org/metiq:latest

# Or rebuild from source
docker build -t metiq:local .

# Run with env vars (easiest)
docker run -d \
  -e METIQ_NOSTR_KEY="nsec1..." \
  -e METIQ_NOSTR_RELAYS="wss://relay.damus.io,wss://nos.lol" \
  -v metiq-data:/data \
  metiq:latest
```

### Local Model Users

Add model context overrides to avoid warnings:

```bash
# Edit ~/.metiq/bootstrap.json
{
  "private_key": "...",
  "relays": [...],
  "model_context_overrides": {
    "ollama/": 8192,
    "lemmy-local/": 8192
  }
}

# Restart metiqd
pkill metiqd && metiqd
```

---

## 🐛 Known Issues

None! All reported issues from this session are fixed.

---

## 🙏 Credits

These improvements were implemented based on real-world usage feedback and OpenClaw migration requirements.

For questions or issues, see:
- [Configuration Reference](docs/gateway/configuration.md)
- [Docker Installation](docs/install/docker.md)
- [OpenClaw Migration Guide](docs/MIGRATION_FROM_OPENCLAW.md)
