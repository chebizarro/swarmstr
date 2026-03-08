package hooks

import (
	"os"
	"path/filepath"
	"strings"
)

// recognisedBootstrapFiles is the set of basenames that are eligible for
// injection, matching OpenClaw's list.
var recognisedBootstrapFiles = map[string]bool{
	"AGENTS.md":     true,
	"SOUL.md":       true,
	"TOOLS.md":      true,
	"IDENTITY.md":   true,
	"USER.md":       true,
	"HEARTBEAT.md":  true,
	"BOOTSTRAP.md":  true,
	"MEMORY.md":     true,
	"memory.md":     true,
}

func makeBootstrapExtraFilesHandler(opts BundledHandlerOpts) HookHandler {
	return func(ev *Event) error {
		if ev.EventType != "agent" || ev.Action != "bootstrap" {
			return nil
		}

		if opts.WorkspaceDir == nil {
			return nil
		}
		workspaceDir := opts.WorkspaceDir()
		if workspaceDir == "" {
			return nil
		}

		// Resolve glob patterns from context.
		var patterns []string
		for _, key := range []string{"paths", "patterns", "files"} {
			if v, ok := ev.Context[key]; ok {
				switch tv := v.(type) {
				case []string:
					patterns = append(patterns, tv...)
				case []any:
					for _, p := range tv {
						if s, ok := p.(string); ok {
							patterns = append(patterns, s)
						}
					}
				}
			}
		}

		var resolved []string
		if opts.ResolvePaths != nil && len(patterns) > 0 {
			paths, err := opts.ResolvePaths(workspaceDir, patterns)
			if err == nil {
				resolved = paths
			}
		} else if len(patterns) > 0 {
			// Simple expansion without glob library.
			for _, pat := range patterns {
				if !filepath.IsAbs(pat) {
					pat = filepath.Join(workspaceDir, pat)
				}
				matches, err := filepath.Glob(pat)
				if err == nil {
					resolved = append(resolved, matches...)
				}
			}
		}

		// Filter to recognised basenames and files that exist and are within workspace.
		var injected []string
		for _, p := range resolved {
			base := filepath.Base(p)
			if !recognisedBootstrapFiles[base] {
				continue
			}
			// Security: must be inside workspaceDir (after resolving symlinks).
			realP, err1 := filepath.EvalSymlinks(p)
			realW, err2 := filepath.EvalSymlinks(workspaceDir)
			if err1 != nil || err2 != nil {
				continue
			}
			if !strings.HasPrefix(realP, realW+string(os.PathSeparator)) && realP != realW {
				continue
			}
			// Read file.
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			injected = append(injected, "# "+base+"\n\n"+string(data))
		}

		if len(injected) > 0 {
			if ev.Context == nil {
				ev.Context = map[string]any{}
			}
			ev.Context["injectedFiles"] = injected
		}
		return nil
	}
}
