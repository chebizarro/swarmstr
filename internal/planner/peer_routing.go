package planner

import (
	"fmt"
	"strings"

	"metiq/internal/store/state"
)

// ── Peer capabilities ───────────────────────────────────────────────────────

// PeerCapability describes what a peer agent can do. This is typically
// built from capability announcements, local registry data, or config.
type PeerCapability struct {
	AgentID       string              `json:"agent_id"`
	Name          string              `json:"name,omitempty"`
	Model         string              `json:"model,omitempty"`
	ToolProfile   string              `json:"tool_profile,omitempty"` // minimal|coding|messaging|full
	EnabledTools  []string            `json:"enabled_tools,omitempty"`
	AutonomyMode  state.AutonomyMode  `json:"autonomy_mode,omitempty"`
	CanDelegate   bool                `json:"can_delegate,omitempty"`
	CanVerify     bool                `json:"can_verify,omitempty"`
	MaxTokens     int                 `json:"max_tokens,omitempty"`
	MaxToolCalls  int                 `json:"max_tool_calls,omitempty"`
	RiskClass     state.RiskClass     `json:"risk_class,omitempty"`
	Tags          []string            `json:"tags,omitempty"` // e.g. "reviewer", "coder", "researcher"
	Available     bool                `json:"available"`
	Meta          map[string]any      `json:"meta,omitempty"`
}

// HasTool reports whether the peer has a specific tool enabled.
func (p PeerCapability) HasTool(tool string) bool {
	if len(p.EnabledTools) == 0 {
		return true // no restriction = all tools
	}
	for _, t := range p.EnabledTools {
		if t == tool {
			return true
		}
	}
	return false
}

// HasTag reports whether the peer has a tag.
func (p PeerCapability) HasTag(tag string) bool {
	tag = strings.ToLower(strings.TrimSpace(tag))
	for _, t := range p.Tags {
		if strings.ToLower(strings.TrimSpace(t)) == tag {
			return true
		}
	}
	return false
}

// ── Task requirements ───────────────────────────────────────────────────────

// TaskRequirements describe what a task needs from its worker.
type TaskRequirements struct {
	// RequiredTools are tools the worker must have.
	RequiredTools []string `json:"required_tools,omitempty"`
	// RequiredTags are tags the worker must match (e.g. "reviewer").
	RequiredTags []string `json:"required_tags,omitempty"`
	// MinAutonomy is the minimum autonomy mode required.
	MinAutonomy state.AutonomyMode `json:"min_autonomy,omitempty"`
	// NeedsDelegation requires the worker to be able to sub-delegate.
	NeedsDelegation bool `json:"needs_delegation,omitempty"`
	// NeedsVerification requires the worker to have verification capability.
	NeedsVerification bool `json:"needs_verification,omitempty"`
	// MinTokenBudget is the minimum token capacity needed.
	MinTokenBudget int `json:"min_token_budget,omitempty"`
	// MinToolCallBudget is the minimum tool call capacity needed.
	MinToolCallBudget int `json:"min_tool_call_budget,omitempty"`
	// MaxRiskClass is the maximum risk class the peer may have.
	MaxRiskClass state.RiskClass `json:"max_risk_class,omitempty"`
	// AllowedAgents restricts candidates to this set (empty = any).
	AllowedAgents []string `json:"allowed_agents,omitempty"`
}

// ── Routing decision ────────────────────────────────────────────────────────

// RoutingDecision is the result of evaluating a peer against task requirements.
type RoutingDecision struct {
	AgentID     string   `json:"agent_id"`
	Eligible    bool     `json:"eligible"`
	Score       float64  `json:"score"` // 0.0–1.0 suitability
	Reasons     []string `json:"reasons,omitempty"`
	Rejections  []string `json:"rejections,omitempty"`
}

// RoutingResult is the output of routing a task to available peers.
type RoutingResult struct {
	Selected    *RoutingDecision  `json:"selected,omitempty"`
	Candidates  []RoutingDecision `json:"candidates"`
	Explanation string            `json:"explanation"`
}

// ── Router ──────────────────────────────────────────────────────────────────

// RoutePeers evaluates all peers against task requirements and returns a
// ranked routing result. The best eligible peer is selected.
func RoutePeers(peers []PeerCapability, reqs TaskRequirements) RoutingResult {
	var decisions []RoutingDecision

	for _, peer := range peers {
		d := evaluatePeer(peer, reqs)
		decisions = append(decisions, d)
	}

	// Find best eligible.
	var best *RoutingDecision
	for i := range decisions {
		if decisions[i].Eligible {
			if best == nil || decisions[i].Score > best.Score {
				best = &decisions[i]
			}
		}
	}

	result := RoutingResult{Candidates: decisions}
	if best != nil {
		result.Selected = best
		result.Explanation = fmt.Sprintf("selected %s (score=%.2f)", best.AgentID, best.Score)
	} else {
		result.Explanation = "no eligible peer found"
	}

	return result
}

// evaluatePeer checks a single peer against requirements and scores it.
func evaluatePeer(peer PeerCapability, reqs TaskRequirements) RoutingDecision {
	d := RoutingDecision{AgentID: peer.AgentID, Eligible: true}

	// Availability.
	if !peer.Available {
		d.Eligible = false
		d.Rejections = append(d.Rejections, "peer not available")
		return d
	}

	// Allowed agents filter.
	if len(reqs.AllowedAgents) > 0 {
		found := false
		for _, a := range reqs.AllowedAgents {
			if a == peer.AgentID {
				found = true
				break
			}
		}
		if !found {
			d.Eligible = false
			d.Rejections = append(d.Rejections, "not in allowed_agents list")
			return d
		}
	}

	score := 1.0

	// Required tools.
	for _, tool := range reqs.RequiredTools {
		if !peer.HasTool(tool) {
			d.Eligible = false
			d.Rejections = append(d.Rejections, fmt.Sprintf("missing required tool %q", tool))
		} else {
			d.Reasons = append(d.Reasons, fmt.Sprintf("has tool %q", tool))
		}
	}

	// Required tags.
	for _, tag := range reqs.RequiredTags {
		if !peer.HasTag(tag) {
			d.Eligible = false
			d.Rejections = append(d.Rejections, fmt.Sprintf("missing required tag %q", tag))
		} else {
			d.Reasons = append(d.Reasons, fmt.Sprintf("has tag %q", tag))
		}
	}

	// Autonomy mode.
	if reqs.MinAutonomy != "" && peer.AutonomyMode != "" {
		// autonomyRank: higher = more restrictive. For peer eligibility,
		// the peer must be at least as permissive (lower rank) as the requirement.
		// A task needing "full" (rank 0) rejects peers with plan_approval (rank 1).
		peerRank := autonomyRank[peer.AutonomyMode]
		reqRank := autonomyRank[reqs.MinAutonomy]
		if peerRank > reqRank {
			d.Eligible = false
			d.Rejections = append(d.Rejections,
				fmt.Sprintf("autonomy %q below required %q", peer.AutonomyMode, reqs.MinAutonomy))
		} else {
			d.Reasons = append(d.Reasons, fmt.Sprintf("autonomy %q meets requirement", peer.AutonomyMode))
		}
	}

	// Delegation capability.
	if reqs.NeedsDelegation && !peer.CanDelegate {
		d.Eligible = false
		d.Rejections = append(d.Rejections, "cannot delegate")
	}

	// Verification capability.
	if reqs.NeedsVerification && !peer.CanVerify {
		d.Eligible = false
		d.Rejections = append(d.Rejections, "cannot verify")
	}

	// Token budget.
	if reqs.MinTokenBudget > 0 && peer.MaxTokens > 0 && peer.MaxTokens < reqs.MinTokenBudget {
		d.Eligible = false
		d.Rejections = append(d.Rejections,
			fmt.Sprintf("token budget %d below required %d", peer.MaxTokens, reqs.MinTokenBudget))
	}

	// Tool call budget.
	if reqs.MinToolCallBudget > 0 && peer.MaxToolCalls > 0 && peer.MaxToolCalls < reqs.MinToolCallBudget {
		d.Eligible = false
		d.Rejections = append(d.Rejections,
			fmt.Sprintf("tool call budget %d below required %d", peer.MaxToolCalls, reqs.MinToolCallBudget))
	}

	// Risk class.
	if reqs.MaxRiskClass != "" && peer.RiskClass != "" {
		if riskRank[peer.RiskClass] > riskRank[reqs.MaxRiskClass] {
			d.Eligible = false
			d.Rejections = append(d.Rejections,
				fmt.Sprintf("risk class %q exceeds max %q", peer.RiskClass, reqs.MaxRiskClass))
		}
	}

	// Scoring: bonus for extra capabilities.
	if peer.CanDelegate {
		score += 0.1
	}
	if peer.CanVerify {
		score += 0.1
	}
	if peer.MaxTokens > 0 && reqs.MinTokenBudget > 0 {
		ratio := float64(peer.MaxTokens) / float64(reqs.MinTokenBudget)
		if ratio > 2.0 {
			score += 0.1 // excess capacity
		}
	}
	// Prefer peers with matching tags.
	if len(reqs.RequiredTags) > 0 {
		matched := 0
		for _, tag := range reqs.RequiredTags {
			if peer.HasTag(tag) {
				matched++
			}
		}
		score += float64(matched) * 0.05
	}

	// Keep raw score — higher is better. No cap needed since we compare
	// relative scores, not absolute values.

	d.Score = score
	return d
}

// autonomyRank and riskRank are defined in authority.go.

// ── Formatting ──────────────────────────────────────────────────────────────

// FormatRoutingResult returns a human-readable routing summary.
func FormatRoutingResult(r RoutingResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Routing: %s\n", r.Explanation)
	if r.Selected != nil {
		fmt.Fprintf(&b, "  → Selected: %s (score=%.2f)\n", r.Selected.AgentID, r.Selected.Score)
	}
	for _, c := range r.Candidates {
		status := "✓"
		if !c.Eligible {
			status = "✗"
		}
		fmt.Fprintf(&b, "  %s %s score=%.2f\n", status, c.AgentID, c.Score)
		for _, reason := range c.Reasons {
			fmt.Fprintf(&b, "      + %s\n", reason)
		}
		for _, rej := range c.Rejections {
			fmt.Fprintf(&b, "      - %s\n", rej)
		}
	}
	return b.String()
}

// FormatRoutingDecision returns a single-peer summary.
func FormatRoutingDecision(d RoutingDecision) string {
	if d.Eligible {
		return fmt.Sprintf("%s: eligible (score=%.2f, reasons=%d)",
			d.AgentID, d.Score, len(d.Reasons))
	}
	return fmt.Sprintf("%s: rejected (%s)",
		d.AgentID, strings.Join(d.Rejections, "; "))
}
