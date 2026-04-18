// Package threadown provides thread ownership tracking for multi-agent
// coordination. It prevents multiple agents from responding in the same
// threaded conversation by tracking which agent "owns" each thread.
//
// Ownership follows a first-come-first-served model: the first agent to
// send a message in a thread claims it. Subsequent sends from other agents
// are blocked unless the agent was recently @-mentioned in the thread.
//
// This is the swarmstr equivalent of openclaw's thread-ownership extension,
// implemented as an in-process tracker rather than an external HTTP service.
package threadown

import (
	"strings"
	"sync"
	"time"
)

// DefaultMentionTTL is how long an @-mention override lasts before expiring.
const DefaultMentionTTL = 5 * time.Minute

// ownerEntry records which agent claimed a thread and when.
type ownerEntry struct {
	AgentID   string
	ClaimedAt time.Time
}

// TrackerConfig configures thread ownership behaviour.
type TrackerConfig struct {
	// MentionTTL is how long an @-mention override lasts. Zero uses DefaultMentionTTL.
	MentionTTL time.Duration

	// OwnerTTL is how long ownership persists. Zero means no expiry (ownership
	// lasts until the tracker is reset or the process restarts).
	OwnerTTL time.Duration

	// EnforcedChannels limits enforcement to specific channel IDs.
	// An empty set means enforcement applies to all channels.
	EnforcedChannels map[string]bool
}

// Tracker manages thread ownership for multi-agent deployments.
// It is safe for concurrent use.
type Tracker struct {
	mu       sync.RWMutex
	owners   map[string]ownerEntry // "channelID\x00threadID" → owner
	mentions map[string]time.Time  // "channelID\x00threadID\x00agentID" → mention time
	cfg      TrackerConfig
}

// NewTracker creates a Tracker with the given configuration.
func NewTracker(cfg TrackerConfig) *Tracker {
	if cfg.MentionTTL <= 0 {
		cfg.MentionTTL = DefaultMentionTTL
	}
	return &Tracker{
		owners:   make(map[string]ownerEntry),
		mentions: make(map[string]time.Time),
		cfg:      cfg,
	}
}

// ── Key helpers ─────────────────────────────────────────────────────────────

func threadKey(channelID, threadID string) string {
	return channelID + "\x00" + threadID
}

func mentionKey(channelID, threadID, agentID string) string {
	return channelID + "\x00" + threadID + "\x00" + agentID
}

// ── Core operations ─────────────────────────────────────────────────────────

// ClaimResult describes the outcome of a thread ownership claim.
type ClaimResult struct {
	// Allowed is true if the agent may send in this thread.
	Allowed bool

	// Owner is the agent ID that owns the thread (may be this agent or another).
	Owner string

	// Reason explains the decision.
	Reason string
}

// Claim attempts to claim ownership of a thread for agentID.
//
// Returns:
//   - Allowed=true if the agent owns (or just claimed) the thread
//   - Allowed=false with Owner set to the competing agent's ID if blocked
//
// Top-level messages (empty threadID) are always allowed.
func (t *Tracker) Claim(agentID, channelID, threadID string) ClaimResult {
	// Top-level messages are always allowed.
	if threadID == "" {
		return ClaimResult{Allowed: true, Owner: agentID, Reason: "top-level message"}
	}

	// If channel enforcement is scoped and this channel is not in scope, allow.
	if len(t.cfg.EnforcedChannels) > 0 && !t.cfg.EnforcedChannels[channelID] {
		return ClaimResult{Allowed: true, Owner: agentID, Reason: "channel not enforced"}
	}

	now := time.Now()
	key := threadKey(channelID, threadID)

	t.mu.Lock()
	defer t.mu.Unlock()

	// Sweep expired entries opportunistically.
	t.sweepExpiredLocked(now)

	existing, exists := t.owners[key]
	if !exists {
		// Unclaimed — claim it.
		t.owners[key] = ownerEntry{AgentID: agentID, ClaimedAt: now}
		return ClaimResult{Allowed: true, Owner: agentID, Reason: "claimed"}
	}

	// Already owned by this agent.
	if existing.AgentID == agentID {
		return ClaimResult{Allowed: true, Owner: agentID, Reason: "already owned"}
	}

	// Owned by another agent — check for @-mention override.
	mKey := mentionKey(channelID, threadID, agentID)
	if mentionTime, hasMention := t.mentions[mKey]; hasMention {
		if now.Sub(mentionTime) < t.cfg.MentionTTL {
			return ClaimResult{Allowed: true, Owner: existing.AgentID, Reason: "mention override"}
		}
		// Expired mention — clean up.
		delete(t.mentions, mKey)
	}

	// Blocked.
	return ClaimResult{
		Allowed: false,
		Owner:   existing.AgentID,
		Reason:  "owned by " + existing.AgentID,
	}
}

// ShouldSend is a convenience wrapper around Claim that returns a simple bool.
// Use Claim() when you need the full decision details.
func (t *Tracker) ShouldSend(agentID, channelID, threadID string) bool {
	return t.Claim(agentID, channelID, threadID).Allowed
}

// TrackMention records that agentID was @-mentioned in a thread. This allows
// the agent to respond even if another agent owns the thread.
func (t *Tracker) TrackMention(agentID, channelID, threadID string) {
	if threadID == "" || agentID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	t.mentions[mentionKey(channelID, threadID, agentID)] = time.Now()
}

// IsMentioned returns true if agentID was recently @-mentioned in the thread.
func (t *Tracker) IsMentioned(agentID, channelID, threadID string) bool {
	if threadID == "" {
		return false
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	mTime, ok := t.mentions[mentionKey(channelID, threadID, agentID)]
	if !ok {
		return false
	}
	return time.Since(mTime) < t.cfg.MentionTTL
}

// Owner returns the agent ID that currently owns a thread, or "" if unclaimed.
func (t *Tracker) Owner(channelID, threadID string) string {
	if threadID == "" {
		return ""
	}

	t.mu.RLock()
	defer t.mu.RUnlock()
	entry, ok := t.owners[threadKey(channelID, threadID)]
	if !ok {
		return ""
	}
	if t.cfg.OwnerTTL > 0 && time.Since(entry.ClaimedAt) >= t.cfg.OwnerTTL {
		return "" // expired
	}
	return entry.AgentID
}

// Reset clears all ownership and mention tracking data.
func (t *Tracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.owners = make(map[string]ownerEntry)
	t.mentions = make(map[string]time.Time)
}

// Stats returns a snapshot of tracker state for debugging/monitoring.
func (t *Tracker) Stats() TrackerStats {
	now := time.Now()
	t.mu.RLock()
	defer t.mu.RUnlock()

	activeOwners := 0
	for _, entry := range t.owners {
		if t.cfg.OwnerTTL <= 0 || now.Sub(entry.ClaimedAt) < t.cfg.OwnerTTL {
			activeOwners++
		}
	}

	activeMentions := 0
	for _, mTime := range t.mentions {
		if now.Sub(mTime) < t.cfg.MentionTTL {
			activeMentions++
		}
	}

	return TrackerStats{
		ActiveOwners:   activeOwners,
		ActiveMentions: activeMentions,
		TotalOwners:    len(t.owners),
		TotalMentions:  len(t.mentions),
	}
}

// TrackerStats holds a point-in-time snapshot of tracker state.
type TrackerStats struct {
	ActiveOwners   int `json:"active_owners"`
	ActiveMentions int `json:"active_mentions"`
	TotalOwners    int `json:"total_owners"`
	TotalMentions  int `json:"total_mentions"`
}

// ── Internal helpers ────────────────────────────────────────────────────────

// sweepExpiredLocked removes expired owners and mentions. Must be called with
// t.mu held for writing.
func (t *Tracker) sweepExpiredLocked(now time.Time) {
	// Sweep expired owners.
	if t.cfg.OwnerTTL > 0 {
		for key, entry := range t.owners {
			if now.Sub(entry.ClaimedAt) >= t.cfg.OwnerTTL {
				delete(t.owners, key)
			}
		}
	}

	// Sweep expired mentions.
	for key, mTime := range t.mentions {
		if now.Sub(mTime) >= t.cfg.MentionTTL {
			delete(t.mentions, key)
		}
	}
}

// ── Mention detection helper ────────────────────────────────────────────────

// DetectMention checks if messageText contains an @-mention of the agent.
// It checks for:
//   - @agentName (case-insensitive)
//   - Direct ID reference patterns like <@botUserID>
//   - nostr: npub or hex pubkey references
func DetectMention(messageText string, agentName string, agentAliases ...string) bool {
	if messageText == "" {
		return false
	}
	lower := strings.ToLower(messageText)

	// Check @agentName.
	if agentName != "" {
		nameLower := strings.ToLower(agentName)
		if strings.Contains(lower, "@"+nameLower) {
			return true
		}
	}

	// Check aliases (bot user IDs, npubs, hex pubkeys, etc.).
	for _, alias := range agentAliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		// Support Slack-style <@USERID> references.
		if strings.Contains(messageText, "<@"+alias+">") {
			return true
		}
		// Support bare alias references (case-insensitive).
		if strings.Contains(lower, strings.ToLower(alias)) {
			return true
		}
	}

	return false
}
