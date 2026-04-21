package main

import (
	stdctx "context"
	"log"
	"os"
	"strings"

	ctxengine "metiq/internal/context"
	"metiq/internal/memory"
)

// trySessionMemoryCompact attempts LLM-free compaction using pre-extracted
// session memory. Returns the CompactResult and true if SM compaction was
// performed, or zero result and false if the caller should fall back to
// regular compaction.
//
// This is the integration bridge between the session memory runtime (which
// extracts and maintains the .md file) and the context engine (which manages
// in-memory conversation history). The flow is:
//
//  1. The session memory runtime has already extracted a summary file.
//  2. This function reads that file and passes it to the context engine's
//     CompactWithSessionMemory method.
//  3. The context engine prunes old messages and stores the summary for
//     injection into the system prompt on future Assemble calls.
//
// No LLM call is made — the session memory was already extracted by the
// background runtime, making compaction instant.
func trySessionMemoryCompact(
	ctx stdctx.Context,
	engine ctxengine.Engine,
	sessionID string,
	sessionMemoryPath string,
) (ctxengine.CompactResult, bool) {
	if engine == nil || strings.TrimSpace(sessionMemoryPath) == "" {
		return ctxengine.CompactResult{}, false
	}

	// Check if the engine supports session memory compaction.
	smCompacter, ok := engine.(ctxengine.SessionMemoryCompacter)
	if !ok {
		return ctxengine.CompactResult{}, false
	}

	// Read the session memory file.
	content, err := readSessionMemoryContent(sessionMemoryPath)
	if err != nil {
		log.Printf("session memory compact: read failed path=%s err=%v", sessionMemoryPath, err)
		return ctxengine.CompactResult{}, false
	}
	if strings.TrimSpace(content) == "" {
		return ctxengine.CompactResult{}, false
	}

	// Check if the session memory is still just the empty template.
	if isSessionMemoryEmpty(content) {
		return ctxengine.CompactResult{}, false
	}

	// Perform LLM-free compaction.
	cr, err := smCompacter.CompactWithSessionMemory(ctx, sessionID, content, ctxengine.DefaultSessionMemoryCompactConfig)
	if err != nil {
		log.Printf("session memory compact: failed session=%s err=%v", sessionID, err)
		return ctxengine.CompactResult{}, false
	}

	return cr, cr.Compacted
}

// readSessionMemoryContent reads and validates the session memory file at the
// given path. Returns the validated content or an error.
func readSessionMemoryContent(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content, err := memory.ValidateSessionMemoryDocument(string(raw), memory.MaxSessionMemoryBytes)
	if err != nil {
		// Fall back to raw content if validation fails — the file may have
		// been written by an older version or manually edited.
		return strings.TrimSpace(string(raw)), nil
	}
	return content, nil
}

// isSessionMemoryEmpty checks if the session memory content is still the
// default empty template (no actual session data has been extracted yet).
func isSessionMemoryEmpty(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return true
	}
	template := strings.TrimSpace(memory.DefaultSessionMemoryTemplate)
	return content == template
}
