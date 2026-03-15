package toolbuiltin

import (
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

	"swarmstr/internal/agent"
)

const maxActiveFileWatches = 20

type FileWatchDelivery func(sessionID, name string, event map[string]any)

type fileWatchEntry struct {
	name       string
	sessionID  string
	path       string
	contains   string
	containsRE string
	recursive  bool
	cancel     context.CancelFunc
	createdAt  time.Time
	maxEvents  int
	received   int
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
	deliver FileWatchDelivery,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[name]; exists {
		return fmt.Errorf("watch %q already exists; remove it first", name)
	}
	if len(r.entries) >= maxActiveFileWatches {
		return fmt.Errorf("maximum of %d active file watches reached", maxActiveFileWatches)
	}
	if _, err := os.Stat(watchPath); err != nil {
		return fmt.Errorf("watch path not found: %w", err)
	}
	var re *regexp.Regexp
	if strings.TrimSpace(containsRegex) != "" {
		compiled, err := regexp.Compile(strings.TrimSpace(containsRegex))
		if err != nil {
			return fmt.Errorf("invalid contains_regex: %w", err)
		}
		re = compiled
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	if recursive {
		if err := addWatchRecursive(watcher, watchPath); err != nil {
			_ = watcher.Close()
			return fmt.Errorf("watch path recursively: %w", err)
		}
	} else if err := watcher.Add(watchPath); err != nil {
		_ = watcher.Close()
		return fmt.Errorf("watch path: %w", err)
	}

	subCtx, cancel := context.WithTimeout(ctx, ttl)
	entry := &fileWatchEntry{
		name:       name,
		sessionID:  sessionID,
		path:       watchPath,
		contains:   contains,
		containsRE: strings.TrimSpace(containsRegex),
		recursive:  recursive,
		cancel:     cancel,
		createdAt:  time.Now(),
		maxEvents:  maxEvents,
	}
	r.entries[name] = entry

	go func() {
		defer func() {
			cancel()
			_ = watcher.Close()
			r.mu.Lock()
			delete(r.entries, name)
			r.mu.Unlock()
		}()
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
						_ = addWatchRecursive(watcher, ev.Name)
						continue
					}
				}
				if contains != "" || re != nil {
					okContains, err := fileMatchesContent(ev.Name, contains, re)
					if err != nil || !okContains {
						continue
					}
				}
				payload := map[string]any{
					"path":       filepath.Clean(ev.Name),
					"op":         opName,
					"watch_path": watchPath,
					"at":         time.Now().Unix(),
				}
				deliver(sessionID, name, payload)
				r.mu.Lock()
				entry.received++
				done := maxEvents > 0 && entry.received >= maxEvents
				r.mu.Unlock()
				if done {
					return
				}
			case <-watcher.Errors:
				// best-effort watcher; errors are dropped and loop continues
			}
		}
	}()
	return nil
}

func (r *FileWatchRegistry) stop(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[name]
	if !ok {
		return fmt.Errorf("watch %q not found", name)
	}
	e.cancel()
	delete(r.entries, name)
	return nil
}

func (r *FileWatchRegistry) list() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]map[string]any, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, map[string]any{
			"name":           e.name,
			"session_id":     e.sessionID,
			"path":           e.path,
			"contains":       e.contains,
			"contains_regex": e.containsRE,
			"recursive":      e.recursive,
			"created_at":     e.createdAt.Unix(),
			"received":       e.received,
			"max_events":     e.maxEvents,
		})
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
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	s := string(b)
	if strings.TrimSpace(contains) != "" && !strings.Contains(s, contains) {
		return false, nil
	}
	if re != nil && !re.MatchString(s) {
		return false, nil
	}
	return true, nil
}

func addWatchRecursive(watcher *fsnotify.Watcher, root string) error {
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return watcher.Add(root)
	}
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		return watcher.Add(path)
	})
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
	Description: "Watch a file or directory path and emit events back to the session when it changes.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"name":           {Type: "string", Description: "Unique watch name."},
			"session_id":     {Type: "string", Description: "Session ID that should receive watch event notifications."},
			"path":           {Type: "string", Description: "File or directory path to watch."},
			"event_types":    {Type: "array", Items: &agent.ToolParamProp{Type: "string"}, Description: "Optional event type filter: create|write|remove|rename|chmod."},
			"contains":       {Type: "string", Description: "Optional substring filter; only emit when changed file content includes this text."},
			"contains_regex": {Type: "string", Description: "Optional regex filter; only emit when changed file content matches this regex."},
			"recursive":      {Type: "boolean", Description: "Optional: when path is a directory, watch all nested subdirectories."},
			"ttl_seconds":    {Type: "number", Description: "Optional watch lifetime in seconds (default 3600)."},
			"max_events":     {Type: "number", Description: "Optional max events before auto-stop (default 100; 0 = unlimited)."},
		},
		Required: []string{"name", "session_id", "path"},
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
		sessionID, _ := args["session_id"].(string)
		watchPath, _ := args["path"].(string)
		if strings.TrimSpace(name) == "" {
			return "", fmt.Errorf("file_watch_add: name is required")
		}
		if strings.TrimSpace(sessionID) == "" {
			return "", fmt.Errorf("file_watch_add: session_id is required")
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
		eventTypes := parseWatchEventTypes(args)
		if err := reg.start(
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
			deliver,
		); err != nil {
			return "", fmt.Errorf("file_watch_add: %w", err)
		}
		out, _ := json.Marshal(map[string]any{
			"watching":       true,
			"name":           strings.TrimSpace(name),
			"session_id":     strings.TrimSpace(sessionID),
			"path":           strings.TrimSpace(watchPath),
			"recursive":      recursive,
			"contains":       strings.TrimSpace(contains),
			"contains_regex": strings.TrimSpace(containsRegex),
			"ttl_seconds":    ttlSec,
			"max_events":     maxEvents,
		})
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
