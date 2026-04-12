// enforcement.go enforces routing, slash-command, and delegation restrictions
// from the effective authority. It closes the gap where restrictions were
// previously soft-policy or documentation-level only.
package planner

import (
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

// ── Command classification ─────────────────────────────────────────────────────

// CommandClass categorises slash commands by their authority impact.
type CommandClass string

const (
	// CommandRead is a purely informational command (status, info, help).
	CommandRead CommandClass = "read"
	// CommandSession manages the current session (new, reset, kill, stop).
	CommandSession CommandClass = "session"
	// CommandRouting changes agent routing (focus, unfocus, spawn).
	CommandRouting CommandClass = "routing"
	// CommandConfig changes configuration (set, unset, model).
	CommandConfig CommandClass = "config"
	// CommandExport exports data (export, compact).
	CommandExport CommandClass = "export"
)

// commandClassifications maps known slash commands to their class.
var commandClassifications = map[string]CommandClass{
	// Read commands.
	"help":   CommandRead,
	"status": CommandRead,
	"info":   CommandRead,
	// Session commands.
	"new":   CommandSession,
	"reset": CommandSession,
	"kill":  CommandSession,
	"stop":  CommandSession,
	// Routing commands.
	"focus":   CommandRouting,
	"unfocus": CommandRouting,
	"spawn":   CommandRouting,
	// Config commands.
	"set":   CommandConfig,
	"unset": CommandConfig,
	"model": CommandConfig,
	"fast":  CommandConfig,
	// Export commands.
	"export":  CommandExport,
	"compact": CommandExport,
}

// ClassifyCommand returns the command class for a slash command name.
// Unknown commands default to CommandRead (safe).
func ClassifyCommand(name string) CommandClass {
	if cls, ok := commandClassifications[strings.ToLower(name)]; ok {
		return cls
	}
	return CommandRead
}

// ── Enforcement engine ─────────────────────────────────────────────────────────

// EnforcementEngine checks proposed operations against the effective authority
// and returns detailed denial/allow decisions.
type EnforcementEngine struct {
	governance *GovernanceEngine
}

// NewEnforcementEngine creates an enforcement engine backed by the given
// governance engine. A nil governance engine uses the default policy.
func NewEnforcementEngine(gov *GovernanceEngine) *EnforcementEngine {
	if gov == nil {
		gov = NewGovernanceEngine(nil)
	}
	return &EnforcementEngine{governance: gov}
}

// EnforcementResult describes whether an operation is permitted.
type EnforcementResult struct {
	// Allowed is true if the operation may proceed.
	Allowed bool `json:"allowed"`
	// Reason explains the decision.
	Reason string `json:"reason"`
	// Suggestion provides guidance when denied (e.g. "request operator approval").
	Suggestion string `json:"suggestion,omitempty"`
}

// ── Slash command enforcement ──────────────────────────────────────────────────

// MayRunCommand checks whether the given slash command is permitted under
// the effective authority. Read commands are always allowed. Routing and
// config commands respect the authority's CanAct and autonomy mode.
func (e *EnforcementEngine) MayRunCommand(auth state.TaskAuthority, command string) EnforcementResult {
	cls := ClassifyCommand(command)

	switch cls {
	case CommandRead, CommandExport:
		// Always allowed — these are side-effect-free.
		return EnforcementResult{Allowed: true, Reason: fmt.Sprintf("/%s is a read/export command", command)}

	case CommandSession:
		// Session management requires CanAct.
		if !auth.CanAct {
			return EnforcementResult{
				Allowed:    false,
				Reason:     fmt.Sprintf("/%s requires can_act=true", command),
				Suggestion: "request operator approval to enable actions",
			}
		}
		return EnforcementResult{Allowed: true, Reason: fmt.Sprintf("/%s permitted (can_act=true)", command)}

	case CommandRouting:
		return e.enforceRouting(auth, command)

	case CommandConfig:
		return e.enforceConfig(auth, command)
	}

	return EnforcementResult{Allowed: true, Reason: "unclassified command defaults to allowed"}
}

func (e *EnforcementEngine) enforceRouting(auth state.TaskAuthority, command string) EnforcementResult {
	// Routing commands require CanAct.
	if !auth.CanAct {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("/%s requires can_act=true", command),
			Suggestion: "request operator approval to enable actions",
		}
	}

	// /spawn additionally requires CanDelegate.
	if command == "spawn" && !auth.CanDelegate {
		return EnforcementResult{
			Allowed:    false,
			Reason:     "/spawn requires can_delegate=true",
			Suggestion: "request operator approval to enable delegation",
		}
	}

	// In supervised mode, routing changes need approval.
	mode := auth.EffectiveAutonomyMode(state.AutonomyFull)
	if mode == state.AutonomySupervised {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("/%s blocked in supervised mode", command),
			Suggestion: "request operator approval before routing changes",
		}
	}

	return EnforcementResult{
		Allowed: true,
		Reason:  fmt.Sprintf("/%s permitted (can_act=true, mode=%s)", command, mode),
	}
}

func (e *EnforcementEngine) enforceConfig(auth state.TaskAuthority, command string) EnforcementResult {
	if !auth.CanAct {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("/%s requires can_act=true", command),
			Suggestion: "request operator approval to enable actions",
		}
	}
	// Config changes are sensitive — require at least plan_approval autonomy.
	mode := auth.EffectiveAutonomyMode(state.AutonomyFull)
	if mode == state.AutonomySupervised || mode == state.AutonomyStepApproval {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("/%s blocked in %s mode", command, mode),
			Suggestion: "config changes require plan_approval or full autonomy",
		}
	}
	return EnforcementResult{
		Allowed: true,
		Reason:  fmt.Sprintf("/%s permitted (can_act=true, mode=%s)", command, mode),
	}
}

// ── Delegation enforcement ─────────────────────────────────────────────────────

// MayDelegate checks whether delegation to the given target agent is
// permitted under the effective authority, including depth checks.
func (e *EnforcementEngine) MayDelegate(auth state.TaskAuthority, targetAgent string, currentDepth int) EnforcementResult {
	// Hard gate: CanDelegate.
	if !auth.CanDelegate {
		return EnforcementResult{
			Allowed:    false,
			Reason:     "can_delegate is false in effective authority",
			Suggestion: "request operator approval to enable delegation",
		}
	}

	// Agent allowlist.
	if !auth.MayDelegateTo(targetAgent) {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("agent %q not in allowed_agents list", targetAgent),
			Suggestion: "update authority to include this agent",
		}
	}

	// Depth check.
	if auth.MaxDelegationDepth > 0 && currentDepth >= auth.MaxDelegationDepth {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("delegation depth %d would exceed max_delegation_depth %d", currentDepth+1, auth.MaxDelegationDepth),
			Suggestion: "reduce delegation chain or increase max_delegation_depth",
		}
	}

	// Governance policy check (may require approval/escalation).
	govDec := e.governance.Evaluate(ActionRequest{
		Action:      ActionDelegation,
		TargetAgent: targetAgent,
		Authority:   auth,
	})
	if !govDec.Allowed() {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("governance policy: %s", govDec.Reason),
			Suggestion: verdictSuggestion(govDec.Verdict),
		}
	}

	return EnforcementResult{
		Allowed: true,
		Reason:  fmt.Sprintf("delegation to %q permitted (depth=%d/%d)", targetAgent, currentDepth, auth.MaxDelegationDepth),
	}
}

// ── Tool enforcement ───────────────────────────────────────────────────────────

// MayUseTool checks whether a specific tool invocation is permitted.
func (e *EnforcementEngine) MayUseTool(auth state.TaskAuthority, tool string) EnforcementResult {
	// Hard gate: CanAct.
	if !auth.CanAct {
		return EnforcementResult{
			Allowed:    false,
			Reason:     "can_act is false in effective authority",
			Suggestion: "request operator approval to enable actions",
		}
	}

	// Tool allowlist/denylist.
	if !auth.MayUseTool(tool) {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("tool %q not permitted by authority", tool),
			Suggestion: "update allowed_tools or remove from denied_tools",
		}
	}

	// Governance policy check with tool risk.
	govDec := e.governance.EvaluateToolCall(auth, tool)
	if !govDec.Allowed() {
		return EnforcementResult{
			Allowed:    false,
			Reason:     fmt.Sprintf("governance policy: %s", govDec.Reason),
			Suggestion: verdictSuggestion(govDec.Verdict),
		}
	}

	return EnforcementResult{
		Allowed: true,
		Reason:  fmt.Sprintf("tool %q permitted", tool),
	}
}

// ── ACP enforcement ────────────────────────────────────────────────────────────

// MayAcceptACPTask checks whether accepting an inbound ACP task is permitted
// under the given authority (the receiving agent's authority).
func (e *EnforcementEngine) MayAcceptACPTask(auth state.TaskAuthority, senderPubkey string) EnforcementResult {
	if !auth.CanAct {
		return EnforcementResult{
			Allowed:    false,
			Reason:     "can_act is false — cannot accept ACP tasks",
			Suggestion: "enable can_act to process inbound ACP work",
		}
	}
	return EnforcementResult{
		Allowed: true,
		Reason:  fmt.Sprintf("ACP task from %s permitted", truncate(senderPubkey, 16)),
	}
}

// MaySendACPDelegate checks whether sending an ACP delegation is permitted.
func (e *EnforcementEngine) MaySendACPDelegate(auth state.TaskAuthority, peerPubkey string, currentDepth int) EnforcementResult {
	return e.MayDelegate(auth, peerPubkey, currentDepth)
}

// ── Helpers ────────────────────────────────────────────────────────────────────

func verdictSuggestion(v GovernanceVerdict) string {
	switch v {
	case VerdictRequireApproval:
		return "request operator approval"
	case VerdictRequireEscalation:
		return "escalate to a higher authority"
	case VerdictDeny:
		return "this action is not permitted"
	default:
		return ""
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
