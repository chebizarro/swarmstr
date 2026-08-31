package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"metiq/internal/permissions"
	"metiq/internal/security/commandanalysis"
)

// approvalExecutionRequest extracts the plan actually used by command-bearing
// tools. Display parsing is intentionally not used as execution authority.
func approvalExecutionRequest(toolName string, args map[string]any) (commandanalysis.ExecutionRequest, bool, error) {
	commandText, displayArgv := approvalCommandDisplay(toolName, args)
	request := commandanalysis.ExecutionRequest{Env: approvalEnvironment(args)}

	if explicit := exactApprovalArgv(args); len(explicit) > 0 {
		request.Argv = explicit
		request.CommandText = commandText
	} else if isShellCommandTool(toolName) {
		command := approvalStringArg(args, "command", "cmd", "script")
		if command == "" {
			return request, false, fmt.Errorf("%s requires an exact command string", toolName)
		}
		request.CommandText = command
	} else if len(displayArgv) > 1 {
		request.Argv = displayArgv
		request.CommandText = commandText
	} else {
		// Handle-only controls and structured helpers may still use the legacy
		// one-shot gate, but they cannot receive durable executable trust.
		return request, false, nil
	}

	if toolUsesDirectory(toolName) {
		request.CWD = approvalStringArg(args, "directory", "cwd", "workdir", "dir")
	}
	if request.CWD == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return request, false, fmt.Errorf("resolve execution cwd: %w", err)
		}
		request.CWD = cwd
	}
	return request, true, nil
}

func exactApprovalArgv(args map[string]any) []string {
	for _, key := range []string{"command_argv", "argv"} {
		switch value := args[key].(type) {
		case []string:
			out := append([]string(nil), value...)
			if validApprovalArgv(out) {
				return out
			}
		case []any:
			out := stringSliceFromAny(value)
			if validApprovalArgv(out) {
				return out
			}
		}
	}
	if value, ok := args["cmd"].([]any); ok {
		out := stringSliceFromAny(value)
		if validApprovalArgv(out) {
			return out
		}
	}
	return nil
}

func validApprovalArgv(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	for _, arg := range argv {
		if strings.TrimSpace(arg) == "" || strings.IndexByte(arg, 0) >= 0 {
			return false
		}
	}
	return true
}

func approvalEnvironment(args map[string]any) map[string]string {
	raw, exists := args["env"]
	if !exists {
		return nil
	}
	out := map[string]string{}
	switch env := raw.(type) {
	case map[string]string:
		for key, value := range env {
			out[key] = value
		}
	case map[string]any:
		for key, value := range env {
			if text, ok := value.(string); ok {
				out[key] = text
			}
		}
	}
	return out
}

func approvalStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isShellCommandTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bash", "shell", "exec", "run_command", "terminal", "sh", "bash_exec", "process_spawn", "process_exec":
		return true
	default:
		return false
	}
}

func toolUsesDirectory(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "process_spawn", "process_exec", "git_status", "git_diff", "test_run", "task":
		return true
	default:
		return false
	}
}

func approvalNodeID(args map[string]any) string {
	return approvalStringArg(args, "node_id", "nodeId")
}

func callerExecPolicy(raw any, defaultTools map[string]bool, fullProfile bool, permission permissions.Behavior, permissionActive bool) map[string]any {
	if permissionActive {
		if permission == permissions.BehaviorAsk {
			return map[string]any{"mode": "ask", "tools": []any{"*"}}
		}
		return map[string]any{"mode": "full"}
	}
	if fullProfile {
		return map[string]any{"mode": "full"}
	}
	policy, _ := raw.(map[string]any)
	policy = cloneMapAny(policy)
	if _, exists := policy["tools"]; !exists {
		tools := make([]any, 0, len(defaultTools))
		for tool := range defaultTools {
			tools = append(tools, tool)
		}
		sort.Slice(tools, func(i, j int) bool { return tools[i].(string) < tools[j].(string) })
		policy["tools"] = tools
	}
	return policy
}

func executionHostExecPolicy(reg *execApprovalsRegistry, nodeID string) map[string]any {
	if reg == nil {
		return nil
	}
	global := reg.GetGlobal()
	if strings.TrimSpace(nodeID) == "" {
		return global
	}
	node := reg.GetNode(nodeID)
	if len(node) == 0 {
		return global
	}
	for key, value := range node {
		global[key] = value
	}
	return global
}
