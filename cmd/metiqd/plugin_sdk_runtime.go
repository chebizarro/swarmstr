package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"metiq/internal/gateway/methods"
	"metiq/internal/permissions"
	pluginmanager "metiq/internal/plugins/manager"
	secretspkg "metiq/internal/secrets"
	"metiq/internal/security/commandanalysis"
)

func pluginRuntimeServices(store *secretspkg.Store) pluginmanager.RuntimeServices {
	return pluginmanager.RuntimeServices{
		ExecApprovalEvaluate: func(_ context.Context, request map[string]any) (map[string]any, error) {
			command, argv := pluginApprovalCommand(request)
			if command == "" && len(argv) == 0 {
				return nil, fmt.Errorf("exec approval evaluate requires command or argv")
			}
			analysis := commandanalysis.Analyze(command, argv)
			registry := controlExecApprovals
			if registry == nil {
				return nil, fmt.Errorf("exec approval registry is not initialized")
			}
			callerPolicy := map[string]any{"mode": "ask"}
			for _, key := range []string{"mode", "security", "ask", "askFallback", "ask_fallback", "allowlist", "timeout_ms"} {
				if value, exists := request[key]; exists {
					callerPolicy[key] = value
				}
			}
			decision, err := permissions.EvaluateExecPolicy(callerPolicy, registry.GetGlobal(), permissions.ExecPolicyRequest{
				Tool: "exec", Signature: analysis.Signature, AllowAlwaysSafe: commandanalysis.IsAllowAlwaysSafe(analysis), PromptAvailable: true,
			})
			if err != nil {
				return nil, err
			}
			result := map[string]any{"allowed": decision.Behavior == permissions.BehaviorAllow, "decision": string(decision.Behavior), "reason": decision.Reason, "effective_policy": pluginRuntimeMap(decision.Effective), "analysis": pluginRuntimeMap(analysis)}
			return result, nil
		},
		ExecApprovalRequest: func(_ context.Context, request map[string]any) (map[string]any, error) {
			registry := controlExecApprovals
			if registry == nil {
				return nil, fmt.Errorf("exec approval registry is not initialized")
			}
			data, err := json.Marshal(request)
			if err != nil {
				return nil, fmt.Errorf("encode exec approval request: %w", err)
			}
			var typed methods.ExecApprovalRequestRequest
			if err := json.Unmarshal(data, &typed); err != nil {
				return nil, fmt.Errorf("decode exec approval request: %w", err)
			}
			typed.Command = strings.TrimSpace(typed.Command)
			if typed.Command == "" && len(typed.CommandArgv) > 0 {
				typed.Command = strings.Join(typed.CommandArgv, " ")
			}
			if typed.Command == "" {
				return nil, fmt.Errorf("exec approval request requires command")
			}
			analysis := commandanalysis.Analyze(typed.Command, typed.CommandArgv)
			executionReq := commandanalysis.ExecutionRequest{CommandText: typed.Command, Argv: typed.CommandArgv, Env: typed.Env}
			if typed.CWD != nil {
				executionReq.CWD = *typed.CWD
			}
			binding, err := commandanalysis.CaptureExecutionBinding(executionReq)
			if err != nil {
				return nil, fmt.Errorf("bind exec approval request: %w", err)
			}
			typed.ExecutionBinding = &binding
			typed.CommandArgv = append([]string(nil), binding.Argv...)
			cwd := binding.CanonicalCWD
			typed.CWD = &cwd
			if typed.AnalysisSignature == "" {
				typed.AnalysisWarnings = analysis.Warnings
				typed.AnalysisSummary = analysis.Summary
				typed.AnalysisSignature = analysis.Signature
				typed.AllowAlwaysAvailable = analysis.AllowAlways
			}
			record, err := registry.RequestDurable(typed)
			if err != nil {
				return nil, err
			}
			return pluginRuntimeMap(record), nil
		},
		ExecApprovalSnapshot: func() map[string]any {
			if controlExecApprovals == nil {
				return map[string]any{}
			}
			return controlExecApprovals.GetGlobal()
		},
		ProviderCredentials: store,
	}
}

func pluginApprovalCommand(request map[string]any) (string, []string) {
	command, _ := request["command"].(string)
	if strings.TrimSpace(command) == "" {
		command, _ = request["command_text"].(string)
	}
	var argv []string
	switch values := request["argv"].(type) {
	case []string:
		argv = append([]string(nil), values...)
	case []any:
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				argv = nil
				break
			}
			argv = append(argv, text)
		}
	}
	return strings.TrimSpace(command), argv
}

func pluginRuntimeMap(value any) map[string]any {
	data, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return map[string]any{}
	}
	return result
}
