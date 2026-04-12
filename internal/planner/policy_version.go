// policy_version.go implements versioned prompt/policy management with
// operator-approved apply and revert.  Each version is an immutable snapshot;
// the log records the full audit history of changes.
package planner

import (
	"fmt"
	"strings"
	"sync"

	"metiq/internal/store/state"
)

// ── Version record ─────────────────────────────────────────────────────────────

// PolicyVersion is an immutable snapshot of a prompt or policy field at a
// point in time.
type PolicyVersion struct {
	VersionID  string `json:"version_id"`
	Sequence   int    `json:"sequence"` // monotonically increasing within a field
	Field      string `json:"field"`    // e.g. "system_prompt", "default_autonomy"
	AgentID    string `json:"agent_id,omitempty"`
	Value      string `json:"value"`
	PreviousID string `json:"previous_id,omitempty"` // pointer to predecessor

	// Provenance.
	ProposalID string `json:"proposal_id,omitempty"`
	AppliedBy  string `json:"applied_by,omitempty"`
	Reason     string `json:"reason,omitempty"`

	// Apply semantics.
	ApplyMode ApplyMode `json:"apply_mode"`

	// Timestamps.
	CreatedAt int64 `json:"created_at"`

	// Active is true when this is the live version for the field.
	Active bool `json:"active"`
}

// ── Version log ────────────────────────────────────────────────────────────────

// PolicyVersionLog maintains the ordered version history for a single
// policy field.  It is safe for concurrent use.
type PolicyVersionLog struct {
	mu       sync.RWMutex
	field    string
	agentID  string
	versions []PolicyVersion
	activeID string // version ID of the currently active version
	nextSeq  int
	prefix   string
}

// NewPolicyVersionLog creates a log for a specific field/agent combination.
func NewPolicyVersionLog(field, agentID, prefix string) *PolicyVersionLog {
	if prefix == "" {
		prefix = "pv"
	}
	return &PolicyVersionLog{
		field:   field,
		agentID: agentID,
		prefix:  prefix,
	}
}

// generateIDLocked returns a new unique version ID and sequence number.
// REQUIRES: l.mu must be held by the caller.
func (l *PolicyVersionLog) generateIDLocked() (string, int) {
	l.nextSeq++
	return fmt.Sprintf("%s-%d", l.prefix, l.nextSeq), l.nextSeq
}

// Current returns the active version, or an empty PolicyVersion if none.
func (l *PolicyVersionLog) Current() PolicyVersion {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for i := len(l.versions) - 1; i >= 0; i-- {
		if l.versions[i].Active {
			return l.versions[i]
		}
	}
	return PolicyVersion{}
}

// Versions returns a snapshot of the full version history, oldest first.
func (l *PolicyVersionLog) Versions() []PolicyVersion {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]PolicyVersion, len(l.versions))
	copy(out, l.versions)
	return out
}

// Len returns the number of versions in the log.
func (l *PolicyVersionLog) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.versions)
}

// GetByID returns a version by its ID.
func (l *PolicyVersionLog) GetByID(versionID string) (PolicyVersion, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, v := range l.versions {
		if v.VersionID == versionID {
			return v, true
		}
	}
	return PolicyVersion{}, false
}

// ── Apply ──────────────────────────────────────────────────────────────────────

// ApplyProposal creates a new version from an approved proposal and makes it
// the active version.  The proposal must be in Approved status.
func (l *PolicyVersionLog) ApplyProposal(
	proposal state.PolicyProposal,
	appliedBy string,
	now int64,
) (PolicyVersion, error) {
	if proposal.Status != state.ProposalStatusApproved {
		return PolicyVersion{}, fmt.Errorf("can only apply approved proposals, got %q", proposal.Status)
	}
	if strings.TrimSpace(proposal.TargetField) != l.field {
		return PolicyVersion{}, fmt.Errorf("proposal targets %q, log tracks %q", proposal.TargetField, l.field)
	}

	mode := classifyApplyMode(l.field)

	l.mu.Lock()
	defer l.mu.Unlock()

	prevID := l.activeID
	l.deactivateCurrentLocked()

	id, seq := l.generateIDLocked()

	v := PolicyVersion{
		VersionID:  id,
		Sequence:   seq,
		Field:      l.field,
		AgentID:    l.agentID,
		Value:      proposal.ProposedValue,
		PreviousID: prevID,
		ProposalID: proposal.ProposalID,
		AppliedBy:  appliedBy,
		Reason:     fmt.Sprintf("applied from proposal %s: %s", proposal.ProposalID, proposal.Title),
		ApplyMode:  mode,
		CreatedAt:  now,
		Active:     true,
	}

	l.versions = append(l.versions, v)
	l.activeID = v.VersionID
	return v, nil
}

// ApplyDirect creates a new version without a proposal (e.g. initial
// bootstrap or operator override).
func (l *PolicyVersionLog) ApplyDirect(
	value string,
	appliedBy string,
	reason string,
	now int64,
) PolicyVersion {
	mode := classifyApplyMode(l.field)

	l.mu.Lock()
	defer l.mu.Unlock()

	prevID := l.activeID
	l.deactivateCurrentLocked()

	id, seq := l.generateIDLocked()

	v := PolicyVersion{
		VersionID:  id,
		Sequence:   seq,
		Field:      l.field,
		AgentID:    l.agentID,
		Value:      value,
		PreviousID: prevID,
		AppliedBy:  appliedBy,
		Reason:     reason,
		ApplyMode:  mode,
		CreatedAt:  now,
		Active:     true,
	}

	l.versions = append(l.versions, v)
	l.activeID = v.VersionID
	return v
}

// ── Revert ─────────────────────────────────────────────────────────────────────

// Revert rolls back to the specified version ID, creating a new version entry
// that records the revert operation (preserving audit history).
func (l *PolicyVersionLog) Revert(
	targetVersionID string,
	revertedBy string,
	reason string,
	now int64,
) (PolicyVersion, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Find the target version.
	var target PolicyVersion
	found := false
	for _, v := range l.versions {
		if v.VersionID == targetVersionID {
			target = v
			found = true
			break
		}
	}
	if !found {
		return PolicyVersion{}, fmt.Errorf("version %q not found", targetVersionID)
	}

	prevID := l.activeID
	l.deactivateCurrentLocked()

	mode := classifyApplyMode(l.field)

	id, seq := l.generateIDLocked()

	v := PolicyVersion{
		VersionID:  id,
		Sequence:   seq,
		Field:      l.field,
		AgentID:    l.agentID,
		Value:      target.Value,
		PreviousID: prevID,
		AppliedBy:  revertedBy,
		Reason:     fmt.Sprintf("reverted to %s: %s", targetVersionID, reason),
		ApplyMode:  mode,
		CreatedAt:  now,
		Active:     true,
	}

	l.versions = append(l.versions, v)
	l.activeID = v.VersionID
	return v, nil
}

// RevertToPrevious rolls back to the version before the current one.
func (l *PolicyVersionLog) RevertToPrevious(
	revertedBy string,
	reason string,
	now int64,
) (PolicyVersion, error) {
	l.mu.RLock()
	cur := l.findActiveLocked()
	l.mu.RUnlock()

	if cur.VersionID == "" {
		return PolicyVersion{}, fmt.Errorf("no active version to revert from")
	}
	if cur.PreviousID == "" {
		return PolicyVersion{}, fmt.Errorf("no previous version to revert to")
	}
	return l.Revert(cur.PreviousID, revertedBy, reason, now)
}

func (l *PolicyVersionLog) findActiveLocked() PolicyVersion {
	for i := len(l.versions) - 1; i >= 0; i-- {
		if l.versions[i].Active {
			return l.versions[i]
		}
	}
	return PolicyVersion{}
}

func (l *PolicyVersionLog) deactivateCurrentLocked() {
	for i := range l.versions {
		if l.versions[i].Active {
			l.versions[i].Active = false
		}
	}
	l.activeID = ""
}

// ── Apply-mode classification ──────────────────────────────────────────────────

// classifyApplyMode determines whether a field change can be hot-applied
// or requires a restart, based on the existing ConfigChangedNeedsRestart
// semantics.
//
// Hot-apply fields: system_prompt, default_autonomy, dm.policy, relays,
// session/heartbeat/tts tunables.
//
// Restart-required fields: default_model, providers, extensions.
func classifyApplyMode(field string) ApplyMode {
	switch field {
	case "system_prompt", "default_autonomy", "dm.policy",
		"relays.read", "relays.write",
		"session", "heartbeat", "tts", "secrets",
		"memory_scope", "enabled_tools", "thinking_level",
		"turn_timeout_secs", "max_context_tokens":
		return ApplyHot
	case "default_model", "providers", "extensions":
		return ApplyRestart
	default:
		// Unknown fields default to next_run for safety.
		return ApplyNextRun
	}
}

// ── Policy version registry ────────────────────────────────────────────────────

// PolicyVersionRegistry manages version logs for multiple fields.
// It is safe for concurrent use.
type PolicyVersionRegistry struct {
	mu   sync.RWMutex
	logs map[string]*PolicyVersionLog // keyed by "field" or "field:agentID"
}

// NewPolicyVersionRegistry creates an empty registry.
func NewPolicyVersionRegistry() *PolicyVersionRegistry {
	return &PolicyVersionRegistry{
		logs: make(map[string]*PolicyVersionLog),
	}
}

func logKey(field, agentID string) string {
	if agentID == "" {
		return field
	}
	return field + ":" + agentID
}

// LogFor returns (or creates) the version log for a field/agent combination.
func (r *PolicyVersionRegistry) LogFor(field, agentID string) *PolicyVersionLog {
	key := logKey(field, agentID)
	r.mu.RLock()
	if l, ok := r.logs[key]; ok {
		r.mu.RUnlock()
		return l
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring write lock.
	if l, ok := r.logs[key]; ok {
		return l
	}
	l := NewPolicyVersionLog(field, agentID, "pv-"+key)
	r.logs[key] = l
	return l
}

// ActiveVersions returns the current active version for each tracked field.
func (r *PolicyVersionRegistry) ActiveVersions() []PolicyVersion {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []PolicyVersion
	for _, l := range r.logs {
		if cur := l.Current(); cur.VersionID != "" {
			out = append(out, cur)
		}
	}
	return out
}

// AllLogs returns the log keys tracked by this registry.
func (r *PolicyVersionRegistry) AllLogs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.logs))
	for k := range r.logs {
		keys = append(keys, k)
	}
	return keys
}

// ── Formatting ─────────────────────────────────────────────────────────────────

// FormatPolicyVersion returns a human-readable summary of a version.
func FormatPolicyVersion(v PolicyVersion) string {
	var b strings.Builder
	active := ""
	if v.Active {
		active = " [ACTIVE]"
	}
	fmt.Fprintf(&b, "Version %s (seq=%d)%s\n", v.VersionID, v.Sequence, active)
	fmt.Fprintf(&b, "  Field: %s", v.Field)
	if v.AgentID != "" {
		fmt.Fprintf(&b, " (agent=%s)", v.AgentID)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "  Apply: %s\n", v.ApplyMode)

	valueSummary := v.Value
	if len(valueSummary) > 80 {
		valueSummary = valueSummary[:77] + "..."
	}
	fmt.Fprintf(&b, "  Value: %s\n", valueSummary)

	if v.ProposalID != "" {
		fmt.Fprintf(&b, "  Proposal: %s\n", v.ProposalID)
	}
	if v.PreviousID != "" {
		fmt.Fprintf(&b, "  Previous: %s\n", v.PreviousID)
	}
	if v.Reason != "" {
		fmt.Fprintf(&b, "  Reason: %s\n", v.Reason)
	}
	return b.String()
}

// FormatVersionHistory returns a formatted version log.
func FormatVersionHistory(versions []PolicyVersion) string {
	if len(versions) == 0 {
		return "No version history."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Version history (%d entries):\n", len(versions))
	for i := len(versions) - 1; i >= 0; i-- {
		v := versions[i]
		active := ""
		if v.Active {
			active = " ◄"
		}
		valueSummary := v.Value
		if len(valueSummary) > 40 {
			valueSummary = valueSummary[:37] + "..."
		}
		fmt.Fprintf(&b, "  %s seq=%d%s: %s\n", v.VersionID, v.Sequence, active, valueSummary)
	}
	return b.String()
}
