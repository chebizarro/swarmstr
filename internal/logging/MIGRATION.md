# Migration Guide: log.Printf → logging Package

## Quick Reference

| Old | New |
|-----|-----|
| `log.Printf("info: %s", msg)` | `logging.LogInfo("info: %s", msg)` |
| `log.Printf("ERROR: %v", err)` | `logging.LogError("%v", err)` |
| `log.Printf("WARNING: %s", msg)` | `logging.LogWarn("%s", msg)` |
| `log.Printf("DEBUG: %s", msg)` | `logging.LogDebug("%s", msg)` |
| `log.Printf("SUCCESS: %s", msg)` | `logging.LogSuccess("%s", msg)` |

## Step-by-Step Migration

### 1. Add Import
```go
import (
    "metiq/internal/logging"
)
```

### 2. Replace Standard Log Calls

**Before:**
```go
log.Printf("acp worker task session link failed session=%s task_id=%s run_id=%s err=%v", 
    sessionID, task.TaskID, run.RunID, err)
```

**After:**
```go
logging.LogError("acp worker task session link failed session=%s task_id=%s run_id=%s err=%v", 
    sessionID, task.TaskID, run.RunID, err)
```

### 3. Use Subsystem Prefixes

**Before:**
```go
log.Printf("[grpc] registered tool: %s", name)
```

**After:**
```go
logging.LogInfo("grpc: registered tool: %s", name)
// The subsystem prefix is automatically colored differently
```

### 4. Categorize by Severity

Look for patterns in existing logs:

**Errors** - Use `LogError`
```go
// Before: log.Printf("ERROR: ...")
// Before: log.Printf("failed to ...")
// Before: log.Printf("panic in ...")
logging.LogError("...")
```

**Warnings** - Use `LogWarn`
```go
// Before: log.Printf("WARNING: ...")
// Before: log.Printf("runtime build warning ...")
logging.LogWarn("...")
```

**Success** - Use `LogSuccess`
```go
// Before: log.Printf("completed ...")
// Before: log.Printf("registered ...")
logging.LogSuccess("...")
```

**Info** - Use `LogInfo`
```go
// Before: log.Printf("starting ...")
// Before: log.Printf("processing ...")
logging.LogInfo("...")
```

**Debug** - Use `LogDebug`
```go
// Before: log.Printf("DEBUG: ...")
// Before: log.Printf("trace: ...")
logging.LogDebug("...")
```

## Automated Migration Examples

### Example 1: Control RPC Errors
**File**: `cmd/metiqd/control_rpc_agents.go`

**Before:**
```go
log.Printf("agents.create: runtime build warning id=%s model=%q err=%v", 
    req.AgentID, req.Model, rtErr)
```

**After:**
```go
logging.LogWarn("agents.create: runtime build warning id=%s model=%q err=%v", 
    req.AgentID, req.Model, rtErr)
```

### Example 2: GRPC Tool Registration
**File**: `cmd/metiqd/grpc_tools.go`

**Before:**
```go
log.Printf("[grpc] registered tool: %s", name)
```

**After:**
```go
logging.LogSuccess("grpc: registered tool: %s", name)
```

### Example 3: Session Cleanup
**File**: `cmd/metiqd/acp_cleanup.go`

**Before:**
```go
log.Printf("acp worker task cleanup failed session=%s task_id=%s err=%v", 
    sessionID, taskID, err)
```

**After:**
```go
logging.LogError("acp worker task cleanup failed session=%s task_id=%s err=%v", 
    sessionID, taskID, err)
```

## Regex Patterns for Search & Replace

### Find Error Logs
```regex
log\.Printf\(".*(?:error|ERROR|failed|panic|Failed).*"
```
Replace with: `logging.LogError(`

### Find Warning Logs
```regex
log\.Printf\(".*(?:warning|WARNING|warn).*"
```
Replace with: `logging.LogWarn(`

### Find Success Logs
```regex
log\.Printf\(".*(?:success|SUCCESS|completed|registered).*"
```
Replace with: `logging.LogSuccess(`

## Testing Your Migration

1. **Run the demo**: `go run cmd/logging-demo/main.go`
2. **Check colors**: Verify output is correctly colored
3. **Test with NO_COLOR**: `NO_COLOR=1 go run cmd/logging-demo/main.go`
4. **Test with FORCE_COLOR**: `FORCE_COLOR=1 go run cmd/logging-demo/main.go`

## Gradual Migration Strategy

You don't have to migrate everything at once:

1. **Start with new code** - Use logging package for all new features
2. **Migrate hot paths** - Update frequently executed code first
3. **Migrate by file** - Pick one file at a time to update
4. **Migrate by subsystem** - Update all logs for one subsystem (e.g., "grpc")

## Benefits After Migration

✓ **Visual clarity** - Different severities are instantly recognizable
✓ **Subsystem tracking** - Easier to filter logs by component
✓ **Consistent styling** - Unified look across the codebase
✓ **Better debugging** - Color-coded output helps spot issues faster
✓ **Professional appearance** - Matches the cyberwave aesthetic

---

Happy migrating! ⚡💜
