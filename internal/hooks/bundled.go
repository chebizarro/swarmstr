package hooks

// RegisterBundledHandlers wires the Go implementations of the 4 bundled hooks
// into the given Manager.  The Hook entries must already be registered (via
// LoadBundledHooks); this function only attaches the Handler field.
func RegisterBundledHandlers(mgr *Manager, opts BundledHandlerOpts) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	for _, h := range mgr.hooks {
		if h.Source != SourceBundled {
			continue
		}
		switch h.HookKey {
		case "session-memory":
			h.Handler = makeSessionMemoryHandler(opts)
		case "bootstrap-extra-files":
			h.Handler = makeBootstrapExtraFilesHandler(opts)
		case "command-logger":
			h.Handler = makeCommandLoggerHandler(opts)
		case "boot-md":
			h.Handler = makeBootMDHandler(opts)
		}
	}
}

// BundledHandlerOpts provides shared dependencies to bundled hook handlers.
type BundledHandlerOpts struct {
	// WorkspaceDir is the base workspace directory (e.g. ~/.swarmstr/workspace).
	// If empty, handlers that need it are no-ops.
	WorkspaceDir func() string

	// LogDir is where command-logger writes its JSONL file.
	// Defaults to ~/.swarmstr/logs.
	LogDir string

	// GenerateSlug calls the agent LLM to produce a short slug from text.
	// May be nil — in that case a timestamp slug is used.
	GenerateSlug func(text string) (string, error)

	// GetTranscript returns recent user+assistant messages for a session.
	GetTranscript func(sessionKey string, limit int) ([]TranscriptMessage, error)

	// RunBootMD executes the markdown body via the agent and returns output.
	// May be nil.
	RunBootMD func(sessionKey, markdown string) error

	// ResolvePaths expands glob patterns relative to workspaceDir.
	// May be nil.
	ResolvePaths func(workspaceDir string, patterns []string) ([]string, error)
}

// TranscriptMessage is a simplified message for memory snapshots.
type TranscriptMessage struct {
	Role    string
	Content string
}
