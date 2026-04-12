// Package planner provides planning, compilation, approval, verification,
// usage tracking, and authority resolution for autonomous agent tasks.
package planner

import (
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

// ── Authority source tracking ──────────────────────────────────────────────────

// AuthoritySource identifies where an authority layer originates.
type AuthoritySource string

const (
	// AuthSourceConfig is the system-wide default from AgentPolicy.
	AuthSourceConfig AuthoritySource = "config"
	// AuthSourceGoal is the authority attached to the parent goal.
	AuthSourceGoal AuthoritySource = "goal"
	// AuthSourceParent is the authority inherited from a parent task.
	AuthSourceParent AuthoritySource = "parent_task"
	// AuthSourceTask is the authority explicitly set on the current task.
	AuthSourceTask AuthoritySource = "task"
	// AuthSourceDelegation is authority carried from an ACP remote parent.
	AuthSourceDelegation AuthoritySource = "delegation"
)

// AuthorityLayer is a single authority input in the resolution chain.
// Layers are applied in order; each successive layer can only narrow
// (never widen) the effective authority.
type AuthorityLayer struct {
	Source    AuthoritySource    `json:"source"`
	Label     string             `json:"label,omitempty"`
	Authority state.TaskAuthority `json:"authority"`
}

// NarrowingRecord documents a single field narrowing for the audit trace.
type NarrowingRecord struct {
	Source AuthoritySource `json:"source"`
	Field  string          `json:"field"`
	From   string          `json:"from"`
	To     string          `json:"to"`
}

// AuthorityTrace records how the effective authority was computed,
// including every layer that contributed and each narrowing event.
type AuthorityTrace struct {
	Layers    []AuthorityLayer    `json:"layers"`
	Effective state.TaskAuthority `json:"effective"`
	Narrowed  []NarrowingRecord   `json:"narrowed,omitempty"`
}

// ── Autonomy ranking ───────────────────────────────────────────────────────────

// autonomyRank maps each autonomy mode to a strictness score.
// Higher number = more restrictive. Used by narrowing to always pick
// the more restrictive mode.
var autonomyRank = map[state.AutonomyMode]int{
	state.AutonomyFull:         0,
	state.AutonomyPlanApproval: 1,
	state.AutonomyStepApproval: 2,
	state.AutonomySupervised:   3,
}

// moreRestrictiveAutonomy returns the stricter of two autonomy modes.
// Unknown modes are treated as maximally restrictive (supervised).
func moreRestrictiveAutonomy(a, b state.AutonomyMode) state.AutonomyMode {
	ra, ok := autonomyRank[a]
	if !ok {
		ra = 3 // unknown → supervised
	}
	rb, ok := autonomyRank[b]
	if !ok {
		rb = 3
	}
	if ra >= rb {
		return a
	}
	return b
}

// riskRank maps each risk class to a severity score.
// Higher number = more severe. Narrowing takes the higher risk.
var riskRank = map[state.RiskClass]int{
	state.RiskClassLow:      0,
	state.RiskClassMedium:   1,
	state.RiskClassHigh:     2,
	state.RiskClassCritical: 3,
}

// higherRisk returns the more severe of two risk classes.
func higherRisk(a, b state.RiskClass) state.RiskClass {
	ra, ok := riskRank[a]
	if !ok {
		ra = 3 // unknown → critical
	}
	rb, ok := riskRank[b]
	if !ok {
		rb = 3
	}
	if ra >= rb {
		return a
	}
	return b
}

// ── Core narrowing ─────────────────────────────────────────────────────────────

// NarrowAuthority produces the strictest combination of parent and child
// authorities. The result can never be wider than the parent.
//
// Narrowing rules:
//   - AutonomyMode: more restrictive wins
//   - RiskClass: higher severity wins
//   - CanAct, CanDelegate, CanEscalate: AND (false if either is false)
//   - EscalationRequired: OR (true if either requires it)
//   - AllowedTools: intersection (only tools in both lists)
//   - DeniedTools: union (denied in either → denied)
//   - AllowedAgents: intersection
//   - MaxDelegationDepth: minimum of both (0 means unlimited, so a set
//     value always wins over 0)
//   - Role: child role wins when set, otherwise inherits parent
func NarrowAuthority(parent, child state.TaskAuthority) state.TaskAuthority {
	result := child

	// Autonomy: more restrictive.
	if parent.AutonomyMode != "" && child.AutonomyMode != "" {
		result.AutonomyMode = moreRestrictiveAutonomy(parent.AutonomyMode, child.AutonomyMode)
	} else if parent.AutonomyMode != "" {
		result.AutonomyMode = parent.AutonomyMode
	}

	// Risk: higher severity.
	if parent.RiskClass != "" && child.RiskClass != "" {
		result.RiskClass = higherRisk(parent.RiskClass, child.RiskClass)
	} else if parent.RiskClass != "" {
		result.RiskClass = parent.RiskClass
	}

	// Boolean gates: AND for permissions, OR for requirements.
	if !parent.CanAct {
		result.CanAct = false
	}
	if !parent.CanDelegate {
		result.CanDelegate = false
	}
	if !parent.CanEscalate {
		result.CanEscalate = false
	}
	if parent.EscalationRequired {
		result.EscalationRequired = true
	}

	// AllowedTools: intersection.
	result.AllowedTools = intersectStrings(parent.AllowedTools, child.AllowedTools)
	// DeniedTools: union.
	result.DeniedTools = unionStrings(parent.DeniedTools, child.DeniedTools)
	// AllowedAgents: intersection.
	result.AllowedAgents = intersectStrings(parent.AllowedAgents, child.AllowedAgents)

	// MaxDelegationDepth: min (0 = unlimited, positive value always wins).
	result.MaxDelegationDepth = narrowDepth(parent.MaxDelegationDepth, child.MaxDelegationDepth)

	// Role: child takes precedence, inherit from parent if unset.
	if result.Role == "" {
		result.Role = parent.Role
	}

	// Clear deprecated field.
	result.ApprovalMode = ""

	return result
}

// narrowDepth returns the stricter of two delegation depth limits.
// 0 means unlimited, so any positive value narrows it.
func narrowDepth(parent, child int) int {
	if parent == 0 {
		return child
	}
	if child == 0 {
		return parent
	}
	if parent < child {
		return parent
	}
	return child
}

// ── Set operations ─────────────────────────────────────────────────────────────

// intersectStrings returns the intersection of two string slices.
// If either is nil/empty, the other's list is inherited (an empty list
// means "unrestricted", so a set list narrows it).
func intersectStrings(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	var result []string
	for _, s := range b {
		if _, ok := set[s]; ok {
			result = append(result, s)
		}
	}
	return result
}

// unionStrings returns the de-duplicated union of two string slices.
func unionStrings(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	var result []string
	for _, s := range a {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	for _, s := range b {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// ── Resolution ─────────────────────────────────────────────────────────────────

// ResolveAuthority computes the effective authority by folding ordered layers.
// Each layer narrows the running authority — the first layer is the broadest
// (typically config defaults) and subsequent layers can only tighten it.
//
// An empty layers list returns a zero-value trace with an empty authority.
func ResolveAuthority(layers ...AuthorityLayer) AuthorityTrace {
	trace := AuthorityTrace{
		Layers: layers,
	}
	if len(layers) == 0 {
		return trace
	}

	effective := layers[0].Authority.Normalize()

	for i := 1; i < len(layers); i++ {
		layer := layers[i]
		child := layer.Authority.Normalize()
		before := effective

		effective = NarrowAuthority(before, child)

		// Record narrowing events for the audit trace.
		trace.Narrowed = append(trace.Narrowed,
			diffAuthority(layer.Source, before, effective)...)
	}

	trace.Effective = effective
	return trace
}

// diffAuthority compares before and after an authority narrowing and returns
// records for each field that changed.
func diffAuthority(source AuthoritySource, before, after state.TaskAuthority) []NarrowingRecord {
	var records []NarrowingRecord

	if before.AutonomyMode != after.AutonomyMode {
		records = append(records, NarrowingRecord{
			Source: source, Field: "autonomy_mode",
			From: string(before.AutonomyMode), To: string(after.AutonomyMode),
		})
	}
	if before.RiskClass != after.RiskClass {
		records = append(records, NarrowingRecord{
			Source: source, Field: "risk_class",
			From: string(before.RiskClass), To: string(after.RiskClass),
		})
	}
	if before.CanAct != after.CanAct {
		records = append(records, NarrowingRecord{
			Source: source, Field: "can_act",
			From: boolStr(before.CanAct), To: boolStr(after.CanAct),
		})
	}
	if before.CanDelegate != after.CanDelegate {
		records = append(records, NarrowingRecord{
			Source: source, Field: "can_delegate",
			From: boolStr(before.CanDelegate), To: boolStr(after.CanDelegate),
		})
	}
	if before.CanEscalate != after.CanEscalate {
		records = append(records, NarrowingRecord{
			Source: source, Field: "can_escalate",
			From: boolStr(before.CanEscalate), To: boolStr(after.CanEscalate),
		})
	}
	if before.EscalationRequired != after.EscalationRequired {
		records = append(records, NarrowingRecord{
			Source: source, Field: "escalation_required",
			From: boolStr(before.EscalationRequired), To: boolStr(after.EscalationRequired),
		})
	}
	if before.MaxDelegationDepth != after.MaxDelegationDepth {
		records = append(records, NarrowingRecord{
			Source: source, Field: "max_delegation_depth",
			From: intStr(before.MaxDelegationDepth), To: intStr(after.MaxDelegationDepth),
		})
	}
	// Tool/agent list changes are captured if lengths differ.
	if len(before.AllowedTools) != len(after.AllowedTools) {
		records = append(records, NarrowingRecord{
			Source: source, Field: "allowed_tools",
			From: intStr(len(before.AllowedTools)), To: intStr(len(after.AllowedTools)),
		})
	}
	if len(before.DeniedTools) != len(after.DeniedTools) {
		records = append(records, NarrowingRecord{
			Source: source, Field: "denied_tools",
			From: intStr(len(before.DeniedTools)), To: intStr(len(after.DeniedTools)),
		})
	}
	if len(before.AllowedAgents) != len(after.AllowedAgents) {
		records = append(records, NarrowingRecord{
			Source: source, Field: "allowed_agents",
			From: intStr(len(before.AllowedAgents)), To: intStr(len(after.AllowedAgents)),
		})
	}

	return records
}

func boolStr(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func intStr(v int) string {
	return fmt.Sprintf("%d", v)
}

// ── Convenience builders ───────────────────────────────────────────────────────

// BuildAuthorityChain constructs the standard resolution chain for a task:
//
//	config defaults → goal authority → parent task authority → task authority
//
// Nil/zero layers are skipped. The caller can append additional layers
// (e.g. AuthSourceDelegation for ACP tasks) before calling ResolveAuthority.
func BuildAuthorityChain(
	configDefault state.TaskAuthority,
	goal *state.GoalSpec,
	parentTask *state.TaskSpec,
	task state.TaskSpec,
) []AuthorityLayer {
	var layers []AuthorityLayer

	// 1. Config defaults (always present).
	layers = append(layers, AuthorityLayer{
		Source:    AuthSourceConfig,
		Label:     "system defaults",
		Authority: configDefault,
	})

	// 2. Goal-level authority.
	if goal != nil && !isZeroAuthority(goal.Authority) {
		layers = append(layers, AuthorityLayer{
			Source:    AuthSourceGoal,
			Label:     goal.GoalID,
			Authority: goal.Authority,
		})
	}

	// 3. Parent task authority (for sub-tasks / delegation chains).
	if parentTask != nil && !isZeroAuthority(parentTask.Authority) {
		layers = append(layers, AuthorityLayer{
			Source:    AuthSourceParent,
			Label:     parentTask.TaskID,
			Authority: parentTask.Authority,
		})
	}

	// 4. Task's own authority.
	if !isZeroAuthority(task.Authority) {
		layers = append(layers, AuthorityLayer{
			Source:    AuthSourceTask,
			Label:     task.TaskID,
			Authority: task.Authority,
		})
	}

	return layers
}

// isZeroAuthority returns true when the authority has no meaningful fields set.
func isZeroAuthority(a state.TaskAuthority) bool {
	return a.AutonomyMode == "" &&
		a.Role == "" &&
		a.RiskClass == "" &&
		!a.CanAct &&
		!a.CanDelegate &&
		!a.CanEscalate &&
		!a.EscalationRequired &&
		len(a.AllowedAgents) == 0 &&
		len(a.AllowedTools) == 0 &&
		len(a.DeniedTools) == 0 &&
		a.MaxDelegationDepth == 0 &&
		a.ApprovalMode == ""
}

// FormatTrace returns a human-readable summary of an authority resolution
// trace, suitable for debug logs and operator surfaces.
func FormatTrace(trace AuthorityTrace) string {
	var b strings.Builder
	b.WriteString("Authority resolution:\n")
	for i, layer := range trace.Layers {
		label := layer.Label
		if label == "" {
			label = string(layer.Source)
		}
		fmt.Fprintf(&b, "  %d. [%s] %s", i+1, layer.Source, label)
		if layer.Authority.AutonomyMode != "" {
			fmt.Fprintf(&b, " (autonomy=%s)", layer.Authority.AutonomyMode)
		}
		b.WriteByte('\n')
	}
	if len(trace.Narrowed) > 0 {
		b.WriteString("Narrowing:\n")
		for _, nr := range trace.Narrowed {
			fmt.Fprintf(&b, "  - [%s] %s: %s → %s\n", nr.Source, nr.Field, nr.From, nr.To)
		}
	}
	eff := trace.Effective
	fmt.Fprintf(&b, "Effective: autonomy=%s risk=%s can_act=%v can_delegate=%v depth=%d",
		eff.AutonomyMode, eff.RiskClass, eff.CanAct, eff.CanDelegate, eff.MaxDelegationDepth)
	if len(eff.AllowedTools) > 0 {
		fmt.Fprintf(&b, " allowed_tools=%v", eff.AllowedTools)
	}
	if len(eff.DeniedTools) > 0 {
		fmt.Fprintf(&b, " denied_tools=%v", eff.DeniedTools)
	}
	return b.String()
}
