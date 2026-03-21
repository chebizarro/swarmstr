package toolbuiltin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"metiq/internal/agent"
)

const (
	maxActiveFileWatches        = 20
	maxFileSizeForContentFilter = 10 * 1024 * 1024 // 10MB
	maxRecursiveDepth           = 100              // warn if more directories
	defaultFileOpTimeout        = 5 * time.Second
	maxLinesDefault             = 0 // 0 = read entire file
)

type FileWatchDelivery func(sessionID, name string, event map[string]any)

type fileWatchEntry struct {
	name        string
	sessionID   string
	path        string
	contains    string
	containsRE  string
	recursive   bool
	cancel      context.CancelFunc
	createdAt   time.Time
	maxEvents   int
	received    int
	dirCount    int // number of directories being watched (for recursive mode)
	maxLines    int // max lines to read for content filtering (0 = all)
	batchEvents int // batch multiple events before delivery (0 = immediate)
	fileTimeout time.Duration
	stopped     bool       // prevents double-cleanup race
	mu          sync.Mutex // protects entry fields
}

type FileWatchRegistry struct {
	mu      sync.Mutex
	entries map[string]*fileWatchEntry
}

func NewFileWatchRegistry() *FileWatchRegistry {
	return &FileWatchRegistry{entries: map[string]*fileWatchEntry{}}
}

func (r *FileWatchRegistry) start(
	ctx context.Context,
	name, sessionID, watchPath string,
	eventTypes map[string]bool,
	ttl time.Duration,
	maxEvents int,
	contains string,
	containsRegex string,
	recursive bool,
	maxLines int,
	batchEvents int,
	fileTimeout time.Duration,
	deliver FileWatchDelivery,
) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; exists {
		return 0, fmt.Errorf("watch %q already exists; remove it first", name)
	}
	if len(r.entries) >= maxActiveFileWatches {
		return 0, fmt.Errorf("maximum of %d active file watches reached", maxActiveFileWatches)
	}
	if _, err := os.Stat(watchPath); err != nil {
		return 0, fmt.Errorf("watch path not found: %w", err)
	}
	var re *regexp.Regexp
	if strings.TrimSpace(containsRegex) != "" {
		compiled, err := regexp.Compile(strings.TrimSpace(containsRegex))
		if err != nil {
			return 0, fmt.Errorf("invalid contains_regex: %w", err)
		}
		re = compiled
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return 0, fmt.Errorf("create watcher: %w", err)
	}
	var dirCount int
	if recursive {
		var err error
		dirCount, err = addWatchRecursive(watcher, watchPath)
		if err != nil {
			_ = watcher.Close()
			return 0, fmt.Errorf("watch path recursively: %w", err)
		}
	} else if err := watcher.Add(watchPath); err != nil {
		_ = watcher.Close()
		return 0, fmt.Errorf("watch path: %w", err)
	}
	if dirCount > maxRecursiveDepth {
		_ = watcher.Close()
		return 0, fmt.Errorf("recursive watch would monitor %d directories (limit: %d); consider watching a more specific subdirectory or using non-recursive mode", dirCount, maxRecursiveDepth)
	}

	if fileTimeout == 0 {
		fileTimeout = defaultFileOpTimeout
	}
	subCtx, cancel := context.WithTimeout(ctx, ttl)
	entry := &fileWatchEntry{
		name:        name,
		sessionID:   sessionID,
		path:        watchPath,
		contains:    contains,
		containsRE:  strings.TrimSpace(containsRegex),
		recursive:   recursive,
		cancel:      cancel,
		createdAt:   time.Now(),
		maxEvents:   maxEvents,
		dirCount:    dirCount,
		maxLines:    maxLines,
		batchEvents: batchEvents,
		fileTimeout: fileTimeout,
	}
	r.entries[name] = entry

	go func() {
		defer func() {
			// Coordinate cleanup to prevent race with stop()
			entry.mu.Lock()
			if entry.stopped {
				entry.mu.Unlock()
				return
			}
			entry.stopped = true
			entry.mu.Unlock()

			cancel()
			_ = watcher.Close()
			r.mu.Lock()
			delete(r.entries, name)
			r.mu.Unlock()
		}()

		var eventBatch []map[string]any
		var batchTimer *time.Timer
		var batchTimerC <-chan time.Time
		if batchEvents > 1 {
			eventBatch = make([]map[string]any, 0, batchEvents)
			batchTimer = time.NewTimer(500 * time.Millisecond)
			batchTimerC = batchTimer.C
			defer batchTimer.Stop()
		}
		for {
			select {
			case <-subCtx.Done():
				return
			case ev, ok := <-watcher.Events:
				if !ok {
					return
				}
				opName, matched := matchWatchOp(ev.Op, eventTypes)
				if !matched {
					continue
				}
				if recursive && ev.Op.Has(fsnotify.Create) {
					if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
						newDirs, _ := addWatchRecursive(watcher, ev.Name)
						r.mu.Lock()
						entry.dirCount += newDirs
						r.mu.Unlock()
						continue
					}
				}
				if contains != "" || re != nil {
					fileCtx, fileCancel := context.WithTimeout(subCtx, entry.fileTimeout)
					okContains, err := fileMatchesContentWithTimeout(fileCtx, ev.Name, contains, re, entry.maxLines)
					fileCancel()
					if err != nil {
						// Deliver error so agent knows filtering failed
						errorPayload := map[string]any{
							"error":      fmt.Sprintf("content filter failed: %v", err),
							"path":       filepath.Clean(ev.Name),
							"watch_path": watchPath,
							"at":         time.Now().Unix(),
						}
						deliver(sessionID, name, errorPayload)
						continue
					}
					if !okContains {
						continue
					}
				}
				payload := map[string]any{
					"path":       filepath.Clean(ev.Name),
					"op":         opName,
					"watch_path": watchPath,
					"at":         time.Now().Unix(),
				}

				// Handle batching if enabled
				if batchEvents > 1 {
					eventBatch = append(eventBatch, payload)
					if len(eventBatch) >= batchEvents {
						deliverBatch(sessionID, name, eventBatch, deliver)
						eventBatch = eventBatch[:0]
						batchTimer.Reset(500 * time.Millisecond)
					}
				} else {
					deliver(sessionID, name, payload)
				}

				entry.mu.Lock()
				entry.received++
				done := maxEvents > 0 && entry.received >= maxEvents
				entry.mu.Unlock()
				if done {
					// Flush any remaining batched events
					if len(eventBatch) > 0 {
						deliverBatch(sessionID, name, eventBatch, deliver)
					}
					return
				}
			case err := <-watcher.Errors:
				if err != nil {
					// Deliver error as event so agent is aware
					errorPayload := map[string]any{
						"error":      err.Error(),
						"watch_path": watchPath,
						"at":         time.Now().Unix(),
					}
					deliver(sessionID, name, errorPayload)
				}
			case <-batchTimerC:
				if len(eventBatch) > 0 {
					deliverBatch(sessionID, name, eventBatch, deliver)
					eventBatch = eventBatch[:0]
					batchTimer.Reset(500 * time.Millisecond)
				}
			}
		}
	}()
	return dirCount, nil
}

func (r *FileWatchRegistry) stop(name string) error {
	r.mu.Lock()
	e, ok := r.entries[name]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("watch %q not found", name)
	}
	r.mu.Unlock()

	// Coordinate with goroutine cleanup to prevent race
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return nil // already stopped
	}
	e.stopped = true
	e.mu.Unlock()

	// Cancel will trigger goroutine cleanup
	e.cancel()

	// Remove from registry
	r.mu.Lock()
	delete(r.entries, name)
	r.mu.Unlock()

	return nil
}

func (r *FileWatchRegistry) list() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, 0, len(r.entries))
	for _, e := range r.entries {
		e.mu.Lock()
		entry := map[string]any{
			"name":           e.name,
			"session_id":     e.sessionID,
			"path":           e.path,
			"contains":       e.contains,
			"contains_regex": e.containsRE,
			"recursive":      e.recursive,
			"created_at":     e.createdAt.Unix(),
			"received":       e.received,
			"max_events":     e.maxEvents,
		}
		if e.recursive {
			entry["dir_count"] = e.dirCount
		}
		if e.maxLines > 0 {
			entry["max_lines"] = e.maxLines
		}
		if e.batchEvents > 1 {
			entry["batch_events"] = e.batchEvents
		}
		e.mu.Unlock()
		out = append(out, entry)
	}
	return out
}

func matchWatchOp(op fsnotify.Op, wanted map[string]bool) (string, bool) {
	check := []struct {
		name string
		flag fsnotify.Op
	}{
		{"create", fsnotify.Create},
		{"write", fsnotify.Write},
		{"remove", fsnotify.Remove},
		{"rename", fsnotify.Rename},
		{"chmod", fsnotify.Chmod},
	}
	for _, c := range check {
		if op.Has(c.flag) && wanted[c.name] {
			return c.name, true
		}
	}
	return "", false
}

func fileMatchesContent(path, contains string, re *regexp.Regexp) (bool, error) {
	return fileMatchesContentWithTimeout(context.Background(), path, contains, re, 0)
}

func fileMatchesContentWithTimeout(ctx context.Context, path, contains string, re *regexp.Regexp, maxLines int) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}

	// Check file size before reading to avoid memory issues
	if maxLines == 0 && info.Size() > maxFileSizeForContentFilter {
		return false, fmt.Errorf("file %q is %d bytes (limit: %d bytes for content filtering); consider using max_lines parameter or removing contains/contains_regex filter", path, info.Size(), maxFileSizeForContentFilter)
	}

	// Use channel to respect context timeout
	type result struct {
		matched bool
		err     error
	}
	resultCh := make(chan result, 1)

	go func() {
		var content string
		if maxLines > 0 {
			// Read only first N lines
			f, err := os.Open(path)
			if err != nil {
				resultCh <- result{false, err}
				return
			}
			defer f.Close()

			var lines []string
			scanner := bufio.NewScanner(f)
			for scanner.Scan() && len(lines) < maxLines {
				lines = append(lines, scanner.Text())
			}
			if err := scanner.Err(); err != nil {
				resultCh <- result{false, err}
				return
			}
			content = strings.Join(lines, "\n")
		} else {
			// Read entire file
			b, err := os.ReadFile(path)
			if err != nil {
				resultCh <- result{false, err}
				return
			}
			content = string(b)
		}

		if strings.TrimSpace(contains) != "" && !strings.Contains(content, contains) {
			resultCh <- result{false, nil}
			return
		}
		if re != nil && !re.MatchString(content) {
			resultCh <- result{false, nil}
			return
		}
		resultCh <- result{true, nil}
	}()

	select {
	case <-ctx.Done():
		return false, fmt.Errorf("file read timeout after %v; consider using max_lines parameter for large files", defaultFileOpTimeout)
	case res := <-resultCh:
		return res.matched, res.err
	}
}

func addWatchRecursive(watcher *fsnotify.Watcher, root string) (int, error) {
	info, err := os.Stat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		return 0, watcher.Add(root)
	}
	count := 0
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		count++
		return watcher.Add(path)
	})
	return count, err
}

func deliverBatch(sessionID, name string, events []map[string]any, deliver FileWatchDelivery) {
	if len(events) == 0 {
		return
	}
	// Deliver as a single batched event
	batchPayload := map[string]any{
		"batch":       true,
		"event_count": len(events),
		"events":      events,
		"at":          time.Now().Unix(),
	}
	deliver(sessionID, name, batchPayload)
}

func parseWatchEventTypes(args map[string]any) map[string]bool {
	out := map[string]bool{
		"create": true,
		"write":  true,
		"remove": true,
		"rename": true,
	}
	raw := toStringSlice(args["event_types"])
	if len(raw) == 0 {
		return out
	}
	out = map[string]bool{}
	for _, e := range raw {
		v := strings.ToLower(strings.TrimSpace(e))
		switch v {
		case "create", "write", "remove", "rename", "chmod":
			out[v] = true
		}
	}
	if len(out) == 0 {
		return map[string]bool{"write": true}
	}
	return out
}

var FileWatchAddDef = agent.ToolDefinition{
	Name:        "file_watch_add",
	Description: "Watch a file or directory path and emit events back to the session when it changes. LIMITATIONS: (1) Maximum 20 concurrent watches system-wide. (2) Content filters (contains/contains_regex) only work on files ≤10MB unless max_lines is set. (3) Recursive mode limited to 100 directories. (4) If both 'contains' and 'contains_regex' are specified, file must match BOTH filters (AND logic). (5) Watches auto-expire after ttl_seconds or max_events. (6) File operations timeout after 5 seconds by default.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"name":                 {Type: "string", Description: "Unique watch name."},
			"session_id":           {Type: "string", Description: "Session ID to receive notifications. Defaults to current session; only needed for cross-session delivery."},
			"path":                 {Type: "string", Description: "File or directory path to watch."},
			"event_types":          {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Optional event type filter: create|write|remove|rename|chmod. Default: create,write,remove,rename."},
			"contains":             {Type: "string", Description: "Optional substring filter; only emit when changed file content includes this text. Use max_lines to avoid reading huge files."},
			"contains_regex":       {Type: "string", Description: "Optional regex filter; only emit when changed file content matches this regex. Use max_lines to avoid reading huge files. If both 'contains' and 'contains_regex' are set, file must match BOTH."},
			"recursive":            {Type: "boolean", Description: "Optional: when path is a directory, watch all nested subdirectories. WARNING: Limited to 100 directories total. OS may have lower limits (e.g., Linux inotify.max_user_watches)."},
			"ttl_seconds":          {Type: "number", Description: "Optional watch lifetime in seconds (default 3600). Watch auto-stops after this duration."},
			"max_events":           {Type: "number", Description: "Optional max events before auto-stop (default 100; 0 = unlimited). Prevents runaway watches on high-activity paths."},
			"max_lines":            {Type: "number", Description: "Optional: for content filters, only read first N lines of file (default 0 = read all). Use this for large log files to avoid memory issues and timeouts."},
			"batch_events":         {Type: "number", Description: "Optional: batch multiple events before delivery (default 0 = immediate). Set to 5-10 for high-activity paths to reduce notification overhead. Events are batched for max 500ms."},
			"file_timeout_seconds": {Type: "number", Description: "Optional: timeout for file read operations in seconds (default 5). Increase for very large files or slow storage."},
		},
		Required: []string{"name", "path"},
	},
}

var FileWatchRemoveDef = agent.ToolDefinition{
	Name:        "file_watch_remove",
	Description: "Stop and remove an active file watch by name.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"name": {Type: "string", Description: "Watch name to remove."},
		},
		Required: []string{"name"},
	},
}

var FileWatchListDef = agent.ToolDefinition{
	Name:        "file_watch_list",
	Description: "List active file watches and basic counters.",
	Parameters:  agent.ToolParameters{Type: "object"},
}

func FileWatchAddTool(reg *FileWatchRegistry, deliver FileWatchDelivery) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		name, _ := args["name"].(string)
		sessionID, err := agent.ResolveSessionIDStrict(ctx, args)
		if err != nil {
			return "", fmt.Errorf("file_watch_add: %w", err)
		}
		watchPath, _ := args["path"].(string)
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("file_watch_add: name is required")
		}
		if strings.TrimSpace(sessionID) == "" {
			return "", fmt.Errorf("file_watch_add: session_id is required (not in args and not in context)")
		}
		if strings.TrimSpace(watchPath) == "" {
			return "", fmt.Errorf("file_watch_add: path is required")
		}
		ttlSec := 3600
		if v, ok := args["ttl_seconds"].(float64); ok && v > 0 {
			ttlSec = int(v)
		}
		maxEvents := 100
		if v, ok := args["max_events"].(float64); ok {
			maxEvents = int(v)
		}
		recursive, _ := args["recursive"].(bool)
		contains, _ := args["contains"].(string)
		containsRegex, _ := args["contains_regex"].(string)
		maxLines := 0
		if v, ok := args["max_lines"].(float64); ok && v > 0 {
			maxLines = int(v)
		}
		batchEvents := 0
		if v, ok := args["batch_events"].(float64); ok && v > 1 {
			batchEvents = int(v)
		}
		fileTimeoutSec := 5
		if v, ok := args["file_timeout_seconds"].(float64); ok && v > 0 {
			fileTimeoutSec = int(v)
		}
		eventTypes := parseWatchEventTypes(args)
		dirCount, err := reg.start(
			ctx,
			strings.TrimSpace(name),
			strings.TrimSpace(sessionID),
			strings.TrimSpace(watchPath),
			eventTypes,
			time.Duration(ttlSec)*time.Second,
			maxEvents,
			strings.TrimSpace(contains),
			strings.TrimSpace(containsRegex),
			recursive,
			maxLines,
			batchEvents,
			time.Duration(fileTimeoutSec)*time.Second,
			deliver,
		)
		if err != nil {
			return "", fmt.Errorf("file_watch_add: %w", err)
		}
		response := map[string]any{
			"watching":       true,
			"name":           strings.TrimSpace(name),
			"session_id":     strings.TrimSpace(sessionID),
			"path":           strings.TrimSpace(watchPath),
			"recursive":      recursive,
			"contains":       strings.TrimSpace(contains),
			"contains_regex": strings.TrimSpace(containsRegex),
			"ttl_seconds":    ttlSec,
			"max_events":     maxEvents,
		}
		if maxLines > 0 {
			response["max_lines"] = maxLines
		}
		if batchEvents > 1 {
			response["batch_events"] = batchEvents
			response["batch_timeout_ms"] = 500
		}
		if fileTimeoutSec != 5 {
			response["file_timeout_seconds"] = fileTimeoutSec
		}
		if recursive && dirCount > 0 {
			response["dir_count"] = dirCount
			if dirCount > maxRecursiveDepth/2 {
				response["warning"] = fmt.Sprintf("Watching %d directories (limit: %d). Consider watching a more specific subdirectory if performance issues occur.", dirCount, maxRecursiveDepth)
			}
		}
		// Warn agent about approaching global limit
		reg.mu.Lock()
		activeCount := len(reg.entries)
		reg.mu.Unlock()
		if activeCount >= maxActiveFileWatches*3/4 {
			response["warning_capacity"] = fmt.Sprintf("Using %d of %d available watch slots. Remove unused watches with file_watch_remove.", activeCount, maxActiveFileWatches)
		}
		out, _ := json.Marshal(response)
		return string(out), nil
	}
}

func FileWatchRemoveTool(reg *FileWatchRegistry) agent.ToolFunc {
	return func(_ context.Context, args map[string]any) (string, error) {
		name, _ := args["name"].(string)
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("file_watch_remove: name is required")
		}
		if err := reg.stop(strings.TrimSpace(name)); err != nil {
			return "", fmt.Errorf("file_watch_remove: %w", err)
		}
		out, _ := json.Marshal(map[string]any{"removed": true, "name": strings.TrimSpace(name)})
		return string(out), nil
	}
}

func FileWatchListTool(reg *FileWatchRegistry) agent.ToolFunc {
	return func(_ context.Context, _ map[string]any) (string, error) {
		out, _ := json.Marshal(reg.list())
		return string(out), nil
	}
}
