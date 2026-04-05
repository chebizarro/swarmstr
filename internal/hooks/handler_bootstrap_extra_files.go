package hooks

import (
	"path/filepath"
	"strings"

	"metiq/internal/agent"
)

func makeBootstrapExtraFilesHandler(opts BundledHandlerOpts) HookHandler {
	return func(ev *Event) error {
		if ev.EventType != "agent" || ev.Action != "bootstrap" {
			return nil
		}
		if opts.WorkspaceDir == nil {
			return nil
		}
		workspaceDir := strings.TrimSpace(opts.WorkspaceDir())
		if workspaceDir == "" {
			return nil
		}

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
		if len(patterns) == 0 {
			return nil
		}

		var resolved []string
		if opts.ResolvePaths != nil {
			paths, err := opts.ResolvePaths(workspaceDir, patterns)
			if err == nil {
				resolved = paths
			}
		} else {
			for _, pat := range patterns {
				candidate := pat
				if !filepath.IsAbs(candidate) {
					candidate = filepath.Join(workspaceDir, candidate)
				}
				matches, err := filepath.Glob(candidate)
				if err == nil {
					resolved = append(resolved, matches...)
				}
			}
		}
		if len(resolved) == 0 {
			return nil
		}

		bootstrapFiles, _ := agent.LoadResolvedBootstrapFiles(workspaceDir, resolved)
		if len(bootstrapFiles) == 0 {
			return nil
		}
		if ev.Context == nil {
			ev.Context = map[string]any{}
		}
		ev.Context["bootstrapFiles"] = bootstrapFiles
		return nil
	}
}
