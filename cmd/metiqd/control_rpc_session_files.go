package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"metiq/internal/gateway/methods"
	"metiq/internal/store/state"
	"metiq/internal/workspace"
)

var sessionFileService = workspace.NewFileService(nil)

func resolveSessionWorkspace(ctx context.Context, cfg state.ConfigDoc, docs *state.DocsRepository, store *state.SessionStore, key, agentID string) (state.SessionEntry, string, error) {
	key = strings.TrimSpace(key)
	if key == "" || store == nil || docs == nil {
		return state.SessionEntry{}, "", fmt.Errorf("session workspace unavailable")
	}
	entry, ok := store.Get(key)
	if !ok {
		return state.SessionEntry{}, "", fmt.Errorf("unknown session")
	}
	doc, err := docs.GetSession(ctx, key)
	if err != nil || boolMeta(doc.Meta, "deleted") {
		return state.SessionEntry{}, "", fmt.Errorf("unknown session")
	}
	ownedAgent := defaultAgentID(entry.AgentID)
	if asserted := strings.TrimSpace(agentID); asserted != "" && defaultAgentID(asserted) != ownedAgent {
		return state.SessionEntry{}, "", fmt.Errorf("agentId does not own session")
	}
	root := workspace.ResolveSessionWorkspaceDir(cfg, entry)
	if strings.TrimSpace(root) == "" {
		return state.SessionEntry{}, "", fmt.Errorf("session workspace unavailable")
	}
	return entry, root, nil
}

func boolMeta(meta map[string]any, key string) bool { value, _ := meta[key].(bool); return value }

func handleSessionsFilesList(ctx context.Context, cfg state.ConfigDoc, docs *state.DocsRepository, transcripts *state.TranscriptRepository, store *state.SessionStore, req methods.SessionsFilesListRequest) (methods.SessionsFilesListResult, error) {
	entry, root, err := resolveSessionWorkspace(ctx, cfg, docs, store, req.SessionKey, req.AgentID)
	if err != nil {
		return methods.SessionsFilesListResult{}, err
	}
	if transcripts == nil {
		return methods.SessionsFilesListResult{}, fmt.Errorf("transcript repository unavailable")
	}
	rows, err := transcripts.ListSessionAll(ctx, entry.SessionID)
	if err != nil {
		return methods.SessionsFilesListResult{}, err
	}
	touched := collectSessionTouchedFiles(rows)
	result, err := sessionFileService.List(ctx, root, req.Path, req.Search, touched)
	if err != nil {
		return methods.SessionsFilesListResult{}, err
	}
	return methods.SessionsFilesListResult{SessionKey: req.SessionKey, Root: result.Root, Files: result.Files, Browser: result.Browser}, nil
}

func handleSessionsFilesGet(ctx context.Context, cfg state.ConfigDoc, docs *state.DocsRepository, store *state.SessionStore, req methods.SessionsFilesGetRequest) (methods.SessionsFilesGetResult, error) {
	_, root, err := resolveSessionWorkspace(ctx, cfg, docs, store, req.SessionKey, req.AgentID)
	if err != nil {
		return methods.SessionsFilesGetResult{}, err
	}
	canonicalRoot, err := sessionFileService.CanonicalRoot(root)
	if err != nil {
		return methods.SessionsFilesGetResult{}, err
	}
	file, err := sessionFileService.Get(ctx, root, req.Path, "read")
	if err != nil {
		return methods.SessionsFilesGetResult{}, err
	}
	return methods.SessionsFilesGetResult{SessionKey: req.SessionKey, Root: canonicalRoot, File: file}, nil
}

func handleSessionsFilesSet(ctx context.Context, cfg state.ConfigDoc, docs *state.DocsRepository, store *state.SessionStore, req methods.SessionsFilesSetRequest) (methods.SessionsFilesGetResult, error) {
	_, root, err := resolveSessionWorkspace(ctx, cfg, docs, store, req.SessionKey, req.AgentID)
	if err != nil {
		return methods.SessionsFilesGetResult{}, err
	}
	canonicalRoot, err := sessionFileService.CanonicalRoot(root)
	if err != nil {
		return methods.SessionsFilesGetResult{}, err
	}
	file, err := sessionFileService.Set(ctx, root, req.Path, req.Content, req.ExpectedHash, "modified")
	if err != nil {
		return methods.SessionsFilesGetResult{}, err
	}
	file.Content = ""
	return methods.SessionsFilesGetResult{SessionKey: req.SessionKey, Root: canonicalRoot, File: file}, nil
}

func handleSessionsFilesReveal(ctx context.Context, cfg state.ConfigDoc, docs *state.DocsRepository, store *state.SessionStore, req methods.SessionsFilesRevealRequest) map[string]any {
	_, root, err := resolveSessionWorkspace(ctx, cfg, docs, store, req.Key, req.AgentID)
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	path, err := sessionFileService.Reveal(ctx, root)
	if err != nil {
		return map[string]any{"ok": false, "path": path, "error": "workspace could not be revealed on this host"}
	}
	return map[string]any{"ok": true, "path": path}
}

var patchFilePattern = regexp.MustCompile(`(?m)^\*\*\* (?:Add|Update|Delete) File: (.+)$`)
var patchMovePattern = regexp.MustCompile(`(?m)^\*\*\* Move to: (.+)$`)

func collectSessionTouchedFiles(entries []state.TranscriptEntryDoc) []workspace.TouchedFile {
	files := map[string]string{}
	add := func(raw, kind string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if files[raw] != "modified" {
			files[raw] = kind
		}
	}
	for _, entry := range entries {
		if entry.Role != "assistant" || entry.Meta == nil {
			continue
		}
		calls := asMapSlice(entry.Meta["tool_calls"])
		if name, _ := entry.Meta["tool_name"].(string); strings.TrimSpace(name) != "" {
			call := map[string]any{"name": name}
			for _, key := range []string{"args", "arguments", "input", "tool_args"} {
				if value, ok := entry.Meta[key]; ok {
					call["arguments"] = value
					break
				}
			}
			calls = append(calls, call)
		}
		for _, call := range calls {
			name, _ := call["name"].(string)
			name = strings.ToLower(strings.TrimSpace(name))
			args := asStringMap(call["arguments"])
			if args == nil {
				args = asStringMap(call["input"])
			}
			if args == nil {
				args = asStringMap(call["args"])
			}
			if args == nil {
				continue
			}
			kind := "read"
			if name == "write" || name == "edit" || name == "apply_patch" {
				kind = "modified"
			} else if name != "read" {
				continue
			}
			for _, key := range []string{"path", "file_path", "filePath", "file"} {
				if value, ok := args[key].(string); ok {
					add(value, kind)
					break
				}
			}
			if name == "apply_patch" {
				if input, _ := args["input"].(string); input != "" {
					for _, match := range patchFilePattern.FindAllStringSubmatch(input, -1) {
						add(match[1], "modified")
					}
					for _, match := range patchMovePattern.FindAllStringSubmatch(input, -1) {
						add(match[1], "modified")
					}
				}
				for _, change := range asMapSlice(args["changes"]) {
					if filePath, _ := change["path"].(string); filePath != "" {
						add(filePath, "modified")
					}
					if move := asStringMap(change["kind"]); move != nil {
						if target, _ := move["move_path"].(string); target != "" {
							add(target, "modified")
						}
						if target, _ := move["movePath"].(string); target != "" {
							add(target, "modified")
						}
					}
				}
			}
		}
	}
	out := make([]workspace.TouchedFile, 0, len(files))
	for name, kind := range files {
		out = append(out, workspace.TouchedFile{Path: name, Kind: kind})
	}
	return out
}

func asMapSlice(value any) []map[string]any {
	raw, _ := value.([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if m := asStringMap(item); m != nil {
			out = append(out, m)
		}
	}
	if typed, ok := value.([]map[string]any); ok {
		out = append(out, typed...)
	}
	return out
}
func asStringMap(value any) map[string]any { m, _ := value.(map[string]any); return m }
