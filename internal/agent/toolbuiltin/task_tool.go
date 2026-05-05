package toolbuiltin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"metiq/internal/agent"
)

const (
	taskActionInspect = "inspect"
	taskActionWrite   = "write"
	taskActionRun     = "run"
)

type taskfileDocument struct {
	Version string                    `yaml:"version,omitempty"`
	Tasks   map[string]taskfileTarget `yaml:"tasks,omitempty"`
}

type taskfileTarget struct {
	Desc     string `yaml:"desc,omitempty"`
	Summary  string `yaml:"summary,omitempty"`
	Internal bool   `yaml:"internal,omitempty"`
	Cmds     []any  `yaml:"cmds,omitempty"`
	Deps     []any  `yaml:"deps,omitempty"`
}

type taskfileTaskSummary struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Internal    bool   `json:"internal,omitempty"`
	HasCmds     bool   `json:"has_cmds,omitempty"`
	HasDeps     bool   `json:"has_deps,omitempty"`
}

var TaskDef = agent.ToolDefinition{
	Name:        "task",
	Description: "Work with go-task Taskfiles. Use this as the preferred backend for repeatable multi-step local execution plans: inspect an existing Taskfile, write/update one, or run a named task with structured output.",
	Parameters: agent.ToolParameters{
		Type: "object",
		Properties: map[string]agent.ToolParamProp{
			"action": {
				Type:        "string",
				Description: "Operation to perform: inspect, write, or run.",
				Enum:        []string{taskActionInspect, taskActionWrite, taskActionRun},
			},
			"path": {
				Type:        "string",
				Description: "Path to the Taskfile (for example ./Taskfile.yml or /tmp/build/Taskfile.yaml).",
			},
			"content": {
				Type:        "string",
				Description: "Full Taskfile YAML content to write when action=write.",
			},
			"task": {
				Type:        "string",
				Description: "Named task to run when action=run.",
			},
			"directory": {
				Type:        "string",
				Description: "Working directory for action=run. Defaults to the Taskfile's parent directory.",
			},
			"vars_json": {
				Type:        "string",
				Description: "Optional JSON object of Task variables for action=run. Converted to CLI args like KEY=value.",
			},
			"timeout_seconds": {
				Type:        "integer",
				Description: "Maximum execution time for action=run in seconds (1-1800). Defaults to 120.",
			},
		},
		Required: []string{"action", "path"},
	},
	ParamAliases: map[string]string{
		"operation":     "action",
		"taskfile":      "path",
		"taskfile_path": "path",
		"file":          "path",
		"name":          "task",
		"cwd":           "directory",
		"dir":           "directory",
		"timeout":       "timeout_seconds",
		"vars":          "vars_json",
		"yaml":          "content",
	},
}

func TaskTool(opts FilesystemOpts) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		action := taskActionValue(args)
		path := strings.TrimSpace(agent.ArgString(args, "path"))
		if action == "" {
			return "", fmt.Errorf("task: action is required")
		}
		if path == "" {
			return "", fmt.Errorf("task: path is required")
		}
		resolvedPath, err := opts.resolvePath(path)
		if err != nil {
			return "", fmt.Errorf("task: %v", err)
		}
		absPath, err := filepath.Abs(resolvedPath)
		if err != nil {
			return "", fmt.Errorf("task: resolve path: %w", err)
		}

		switch action {
		case taskActionInspect:
			return inspectTaskfile(absPath)
		case taskActionWrite:
			return writeTaskfile(absPath, agent.ArgString(args, "content"))
		case taskActionRun:
			return runTaskfile(ctx, opts, absPath, args)
		default:
			return "", fmt.Errorf("task: unsupported action %q", action)
		}
	}
}

func TaskActionReadOnly(args map[string]any) bool {
	return taskActionValue(args) == taskActionInspect
}

func TaskActionDestructive(args map[string]any) bool {
	action := taskActionValue(args)
	return action == taskActionRun || action == taskActionWrite || action == "execute"
}

func taskActionValue(args map[string]any) string {
	return strings.ToLower(strings.TrimSpace(agent.ArgString(args, "action")))
}

func inspectTaskfile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("task: read %s: %w", path, err)
	}
	doc, err := parseTaskfile(raw)
	if err != nil {
		return "", err
	}
	result := map[string]any{
		"action":     taskActionInspect,
		"path":       path,
		"directory":  filepath.Dir(path),
		"version":    strings.TrimSpace(doc.Version),
		"task_count": len(doc.Tasks),
		"tasks":      summarizeTaskTargets(doc.Tasks),
	}
	return marshalTaskResult(result)
}

func writeTaskfile(path, content string) (string, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return "", fmt.Errorf("task: content is required for write")
	}
	doc, err := parseTaskfile([]byte(content))
	if err != nil {
		return "", err
	}
	if len(doc.Tasks) == 0 {
		return "", fmt.Errorf("task: Taskfile must define at least one task")
	}
	_, statErr := os.Stat(path)
	existed := statErr == nil
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("task: create parent dirs: %w", err)
	}
	if err := os.WriteFile(path, []byte(content+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("task: write %s: %w", path, err)
	}
	result := map[string]any{
		"action":     taskActionWrite,
		"path":       path,
		"directory":  filepath.Dir(path),
		"existed":    existed,
		"task_count": len(doc.Tasks),
		"tasks":      summarizeTaskTargets(doc.Tasks),
	}
	return marshalTaskResult(result)
}

func runTaskfile(ctx context.Context, opts FilesystemOpts, path string, args map[string]any) (string, error) {
	taskName := strings.TrimSpace(agent.ArgString(args, "task"))
	if taskName == "" {
		return "", fmt.Errorf("task: task is required for run")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("task: read %s: %w", path, err)
	}
	doc, err := parseTaskfile(raw)
	if err != nil {
		return "", err
	}
	if _, ok := doc.Tasks[taskName]; !ok {
		return "", fmt.Errorf("task: task %q not found in %s", taskName, path)
	}
	binary, err := exec.LookPath("task")
	if err != nil {
		return "", fmt.Errorf("task: go-task binary not found in PATH")
	}
	directory := strings.TrimSpace(agent.ArgString(args, "directory"))
	if directory == "" {
		directory = filepath.Dir(path)
	}
	resolvedDir, err := opts.resolvePath(directory)
	if err != nil {
		return "", fmt.Errorf("task: %v", err)
	}
	absDir, err := filepath.Abs(resolvedDir)
	if err != nil {
		return "", fmt.Errorf("task: resolve directory: %w", err)
	}
	vars, err := parseTaskVars(agent.ArgString(args, "vars_json"))
	if err != nil {
		return "", err
	}
	argv := []string{"--taskfile", path, taskName}
	argv = append(argv, buildTaskVarArgs(vars)...)

	timeout := 120 * time.Second
	if seconds := agent.ArgInt(args, "timeout_seconds", 0); seconds > 0 {
		if seconds > 1800 {
			seconds = 1800
		}
		timeout = time.Duration(seconds) * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.CommandContext(runCtx, binary, argv...)
	cmd.Dir = absDir
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	startedAt := time.Now()
	err = cmd.Run()
	elapsed := time.Since(startedAt)
	result := map[string]any{
		"action":      taskActionRun,
		"path":        path,
		"directory":   absDir,
		"task":        taskName,
		"vars":        vars,
		"command":     append([]string{binary}, argv...),
		"stdout":      strings.TrimRight(stdoutBuf.String(), "\n"),
		"stderr":      strings.TrimRight(stderrBuf.String(), "\n"),
		"exit_code":   0,
		"duration_ms": elapsed.Milliseconds(),
	}
	if err != nil {
		if runCtx.Err() != nil {
			result["exit_code"] = -1
			result["timed_out"] = true
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result["exit_code"] = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("task: execute: %w", err)
		}
	}
	return marshalTaskResult(result)
}

func parseTaskfile(raw []byte) (taskfileDocument, error) {
	var doc taskfileDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return taskfileDocument{}, fmt.Errorf("task: invalid Taskfile YAML: %w", err)
	}
	if len(doc.Tasks) == 0 {
		return taskfileDocument{}, fmt.Errorf("task: Taskfile must define a non-empty tasks map")
	}
	return doc, nil
}

func summarizeTaskTargets(tasks map[string]taskfileTarget) []taskfileTaskSummary {
	if len(tasks) == 0 {
		return nil
	}
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]taskfileTaskSummary, 0, len(names))
	for _, name := range names {
		t := tasks[name]
		desc := strings.TrimSpace(t.Desc)
		if desc == "" {
			desc = strings.TrimSpace(t.Summary)
		}
		out = append(out, taskfileTaskSummary{
			Name:        name,
			Description: desc,
			Internal:    t.Internal,
			HasCmds:     len(t.Cmds) > 0,
			HasDeps:     len(t.Deps) > 0,
		})
	}
	return out
}

func parseTaskVars(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("task: invalid vars_json: %w", err)
	}
	result := make(map[string]string, len(parsed))
	for key, value := range parsed {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		result[key] = fmt.Sprintf("%v", value)
	}
	return result, nil
}

func buildTaskVarArgs(vars map[string]string) []string {
	if len(vars) == 0 {
		return nil
	}
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, fmt.Sprintf("%s=%s", key, vars[key]))
	}
	return args
}

func marshalTaskResult(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
