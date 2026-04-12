package planner

import (
	"testing"

	"metiq/internal/store/state"
)

// ── ClassifyCommand tests ──────────────────────────────────────────────────────

func TestClassifyCommand_Known(t *testing.T) {
	cases := map[string]CommandClass{
		"help": CommandRead, "status": CommandRead, "info": CommandRead,
		"new": CommandSession, "reset": CommandSession, "kill": CommandSession, "stop": CommandSession,
		"focus": CommandRouting, "unfocus": CommandRouting, "spawn": CommandRouting,
		"set": CommandConfig, "unset": CommandConfig, "model": CommandConfig, "fast": CommandConfig,
		"export": CommandExport, "compact": CommandExport,
	}
	for cmd, want := range cases {
		if got := ClassifyCommand(cmd); got != want {
			t.Errorf("ClassifyCommand(%q) = %s, want %s", cmd, got, want)
		}
	}
}

func TestClassifyCommand_Unknown(t *testing.T) {
	if got := ClassifyCommand("unknown_cmd"); got != CommandRead {
		t.Errorf("unknown command should default to read, got %s", got)
	}
}

func TestClassifyCommand_CaseInsensitive(t *testing.T) {
	if got := ClassifyCommand("HELP"); got != CommandRead {
		t.Errorf("should be case-insensitive, got %s", got)
	}
}

// ── MayRunCommand: read commands ───────────────────────────────────────────────

func TestMayRunCommand_ReadAlwaysAllowed(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	// Even with CanAct=false, read commands should work.
	auth := state.TaskAuthority{CanAct: false}
	for _, cmd := range []string{"help", "status", "info", "export", "compact"} {
		res := eng.MayRunCommand(auth, cmd)
		if !res.Allowed {
			t.Errorf("/%s should be allowed even with CanAct=false: %s", cmd, res.Reason)
		}
	}
}

// ── MayRunCommand: session commands ────────────────────────────────────────────

func TestMayRunCommand_SessionRequiresCanAct(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	noAct := state.TaskAuthority{CanAct: false}
	for _, cmd := range []string{"new", "reset", "kill", "stop"} {
		res := eng.MayRunCommand(noAct, cmd)
		if res.Allowed {
			t.Errorf("/%s should require can_act=true", cmd)
		}
	}

	canAct := state.TaskAuthority{AutonomyMode: state.AutonomyFull, CanAct: true}
	for _, cmd := range []string{"new", "reset", "kill", "stop"} {
		res := eng.MayRunCommand(canAct, cmd)
		if !res.Allowed {
			t.Errorf("/%s should be allowed with can_act=true: %s", cmd, res.Reason)
		}
	}
}

// ── MayRunCommand: routing commands ────────────────────────────────────────────

func TestMayRunCommand_RoutingRequiresCanAct(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	noAct := state.TaskAuthority{CanAct: false}
	res := eng.MayRunCommand(noAct, "focus")
	if res.Allowed {
		t.Error("/focus should require can_act=true")
	}
}

func TestMayRunCommand_SpawnRequiresCanDelegate(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	noDelegate := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		CanAct:       true,
		CanDelegate:  false,
	}
	res := eng.MayRunCommand(noDelegate, "spawn")
	if res.Allowed {
		t.Error("/spawn should require can_delegate=true")
	}

	canDelegate := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		CanAct:       true,
		CanDelegate:  true,
	}
	res = eng.MayRunCommand(canDelegate, "spawn")
	if !res.Allowed {
		t.Errorf("/spawn should be allowed: %s", res.Reason)
	}
}

func TestMayRunCommand_RoutingBlockedInSupervised(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	supervised := state.TaskAuthority{
		AutonomyMode: state.AutonomySupervised,
		CanAct:       true,
		CanDelegate:  true,
	}
	for _, cmd := range []string{"focus", "unfocus", "spawn"} {
		res := eng.MayRunCommand(supervised, cmd)
		if res.Allowed {
			t.Errorf("/%s should be blocked in supervised mode", cmd)
		}
	}
}

func TestMayRunCommand_RoutingAllowedInPlanApproval(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyPlanApproval,
		CanAct:       true,
		CanDelegate:  true,
	}
	for _, cmd := range []string{"focus", "unfocus", "spawn"} {
		res := eng.MayRunCommand(auth, cmd)
		if !res.Allowed {
			t.Errorf("/%s should be allowed in plan_approval: %s", cmd, res.Reason)
		}
	}
}

// ── MayRunCommand: config commands ─────────────────────────────────────────────

func TestMayRunCommand_ConfigRequiresCanAct(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	noAct := state.TaskAuthority{CanAct: false}
	res := eng.MayRunCommand(noAct, "set")
	if res.Allowed {
		t.Error("/set should require can_act=true")
	}
}

func TestMayRunCommand_ConfigBlockedInSupervised(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomySupervised,
		CanAct:       true,
	}
	res := eng.MayRunCommand(auth, "set")
	if res.Allowed {
		t.Error("/set should be blocked in supervised mode")
	}
}

func TestMayRunCommand_ConfigBlockedInStepApproval(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyStepApproval,
		CanAct:       true,
	}
	res := eng.MayRunCommand(auth, "model")
	if res.Allowed {
		t.Error("/model should be blocked in step_approval mode")
	}
}

func TestMayRunCommand_ConfigAllowedInPlanApproval(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyPlanApproval,
		CanAct:       true,
	}
	res := eng.MayRunCommand(auth, "set")
	if !res.Allowed {
		t.Errorf("/set should be allowed in plan_approval: %s", res.Reason)
	}
}

// ── MayDelegate tests ──────────────────────────────────────────────────────────

func TestMayDelegate_CanDelegateFalse(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{CanDelegate: false}
	res := eng.MayDelegate(auth, "worker", 0)
	if res.Allowed {
		t.Error("should deny when can_delegate=false")
	}
}

func TestMayDelegate_AgentNotInAllowlist(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		CanDelegate:   true,
		AllowedAgents: []string{"worker-a"},
	}
	res := eng.MayDelegate(auth, "worker-b", 0)
	if res.Allowed {
		t.Error("should deny non-allowed agent")
	}
}

func TestMayDelegate_DepthExceeded(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode:       state.AutonomyFull,
		RiskClass:          state.RiskClassLow,
		CanDelegate:        true,
		MaxDelegationDepth: 2,
	}
	res := eng.MayDelegate(auth, "worker", 2) // depth=2, max=2 → exceeds
	if res.Allowed {
		t.Error("should deny when depth exceeds max")
	}
}

func TestMayDelegate_DepthWithinLimit(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode:       state.AutonomyFull,
		RiskClass:          state.RiskClassLow,
		CanDelegate:        true,
		MaxDelegationDepth: 3,
	}
	res := eng.MayDelegate(auth, "worker", 1)
	if !res.Allowed {
		t.Errorf("should allow delegation within depth: %s", res.Reason)
	}
}

func TestMayDelegate_UnlimitedDepth(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode:       state.AutonomyFull,
		RiskClass:          state.RiskClassLow,
		CanDelegate:        true,
		MaxDelegationDepth: 0, // unlimited
	}
	res := eng.MayDelegate(auth, "worker", 100)
	if !res.Allowed {
		t.Errorf("unlimited depth should allow: %s", res.Reason)
	}
}

func TestMayDelegate_GovernancePolicyBlocksHighRisk(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomySupervised, // supervised = approval needed
		RiskClass:    state.RiskClassLow,
		CanDelegate:  true,
	}
	res := eng.MayDelegate(auth, "worker", 0)
	if res.Allowed {
		t.Error("supervised mode should block delegation via governance policy")
	}
}

// ── MayUseTool tests ───────────────────────────────────────────────────────────

func TestMayUseTool_CanActFalse(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{CanAct: false}
	res := eng.MayUseTool(auth, "read_file")
	if res.Allowed {
		t.Error("should deny when can_act=false")
	}
}

func TestMayUseTool_DeniedTool(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		CanAct:      true,
		DeniedTools: []string{"dangerous"},
	}
	res := eng.MayUseTool(auth, "dangerous")
	if res.Allowed {
		t.Error("should deny tool in denied list")
	}
}

func TestMayUseTool_AllowedInFullLowRisk(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomyFull,
		RiskClass:    state.RiskClassLow,
		CanAct:       true,
	}
	res := eng.MayUseTool(auth, "read_file")
	if !res.Allowed {
		t.Errorf("should allow: %s", res.Reason)
	}
}

func TestMayUseTool_GovernancePolicyBlocksHighRiskInRestricted(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomySupervised,
		CanAct:       true,
	}
	// Even a read-only tool requires approval in supervised mode.
	res := eng.MayUseTool(auth, "nostr_fetch")
	if res.Allowed {
		t.Error("supervised mode should block tools via governance policy")
	}
}

// ── ACP enforcement tests ──────────────────────────────────────────────────────

func TestMayAcceptACPTask_CanActFalse(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{CanAct: false}
	res := eng.MayAcceptACPTask(auth, "abc123")
	if res.Allowed {
		t.Error("should deny ACP acceptance when can_act=false")
	}
}

func TestMayAcceptACPTask_CanActTrue(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{CanAct: true}
	res := eng.MayAcceptACPTask(auth, "abc123")
	if !res.Allowed {
		t.Errorf("should allow ACP acceptance: %s", res.Reason)
	}
}

func TestMaySendACPDelegate_DelegatesToMayDelegate(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{CanDelegate: false}
	res := eng.MaySendACPDelegate(auth, "peer123", 0)
	if res.Allowed {
		t.Error("should deny ACP delegation when can_delegate=false")
	}
}

// ── End-to-end: resolved authority + enforcement ───────────────────────────────

func TestEndToEnd_RestrictedAuthority_BlocksUnsafe(t *testing.T) {
	// Resolve: config(full) → goal(plan_approval, deny deploy tool)
	config := AuthorityLayer{
		Source: AuthSourceConfig,
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyFull,
			CanAct:       true,
			CanDelegate:  true,
			RiskClass:    state.RiskClassLow,
			AllowedTools: []string{"read_file", "write_file", "deploy"},
		},
	}
	goal := AuthorityLayer{
		Source: AuthSourceGoal,
		Authority: state.TaskAuthority{
			AutonomyMode: state.AutonomyPlanApproval,
			CanAct:       true,
			CanDelegate:  true,
			DeniedTools:  []string{"deploy"},
		},
	}
	trace := ResolveAuthority(config, goal)
	eng := NewEnforcementEngine(nil)

	// read_file: allowed
	readRes := eng.MayUseTool(trace.Effective, "read_file")
	if !readRes.Allowed {
		t.Errorf("read_file should be allowed: %s", readRes.Reason)
	}

	// deploy: denied (denied tools)
	deployRes := eng.MayUseTool(trace.Effective, "deploy")
	if deployRes.Allowed {
		t.Error("deploy should be denied")
	}

	// /help: always allowed
	helpRes := eng.MayRunCommand(trace.Effective, "help")
	if !helpRes.Allowed {
		t.Error("/help should always be allowed")
	}

	// /spawn: allowed (can_delegate=true, plan_approval mode allows routing)
	spawnRes := eng.MayRunCommand(trace.Effective, "spawn")
	if !spawnRes.Allowed {
		t.Errorf("/spawn should be allowed: %s", spawnRes.Reason)
	}
}

func TestEndToEnd_SupervisedBlocks_EverythingExceptReads(t *testing.T) {
	auth := state.TaskAuthority{
		AutonomyMode: state.AutonomySupervised,
		CanAct:       true,
		CanDelegate:  true,
		RiskClass:    state.RiskClassMedium,
	}
	eng := NewEnforcementEngine(nil)

	// Read: allowed.
	if !eng.MayRunCommand(auth, "help").Allowed {
		t.Error("/help should be allowed in supervised")
	}
	// Routing: blocked.
	if eng.MayRunCommand(auth, "focus").Allowed {
		t.Error("/focus should be blocked in supervised")
	}
	// Config: blocked.
	if eng.MayRunCommand(auth, "set").Allowed {
		t.Error("/set should be blocked in supervised")
	}
	// Tools: blocked by governance.
	if eng.MayUseTool(auth, "write_file").Allowed {
		t.Error("tools should be blocked in supervised")
	}
	// Delegation: blocked by governance.
	if eng.MayDelegate(auth, "worker", 0).Allowed {
		t.Error("delegation should be blocked in supervised")
	}
}

// ── Suggestion tests ───────────────────────────────────────────────────────────

func TestEnforcementResult_HasSuggestion(t *testing.T) {
	eng := NewEnforcementEngine(nil)
	auth := state.TaskAuthority{CanAct: false}
	res := eng.MayRunCommand(auth, "reset")
	if res.Suggestion == "" {
		t.Error("denied result should include a suggestion")
	}
}

// ── truncate helper ────────────────────────────────────────────────────────────

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("should not truncate short string: %q", got)
	}
	if got := truncate("a very long string", 5); got != "a ver..." {
		t.Errorf("should truncate: %q", got)
	}
}
