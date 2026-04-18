package threadown

import (
	"sync"
	"testing"
	"time"
)

func TestNewTracker_DefaultMentionTTL(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	if tr.cfg.MentionTTL != DefaultMentionTTL {
		t.Fatalf("MentionTTL = %v, want %v", tr.cfg.MentionTTL, DefaultMentionTTL)
	}
}

func TestNewTracker_CustomMentionTTL(t *testing.T) {
	tr := NewTracker(TrackerConfig{MentionTTL: 10 * time.Second})
	if tr.cfg.MentionTTL != 10*time.Second {
		t.Fatalf("MentionTTL = %v, want 10s", tr.cfg.MentionTTL)
	}
}

// ── Claim ───────────────────────────────────────────────────────────────────

func TestClaim_TopLevelAlwaysAllowed(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	r := tr.Claim("agent-a", "slack", "")
	if !r.Allowed {
		t.Fatal("top-level messages should always be allowed")
	}
	if r.Reason != "top-level message" {
		t.Fatalf("Reason = %q", r.Reason)
	}
}

func TestClaim_FirstAgentClaims(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	r := tr.Claim("agent-a", "slack", "thread-1")
	if !r.Allowed || r.Owner != "agent-a" || r.Reason != "claimed" {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestClaim_SameAgentAllowed(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	tr.Claim("agent-a", "slack", "thread-1")
	r := tr.Claim("agent-a", "slack", "thread-1")
	if !r.Allowed || r.Owner != "agent-a" || r.Reason != "already owned" {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestClaim_DifferentAgentBlocked(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	tr.Claim("agent-a", "slack", "thread-1")
	r := tr.Claim("agent-b", "slack", "thread-1")
	if r.Allowed {
		t.Fatal("different agent should be blocked")
	}
	if r.Owner != "agent-a" {
		t.Fatalf("Owner = %q, want agent-a", r.Owner)
	}
	if r.Reason != "owned by agent-a" {
		t.Fatalf("Reason = %q", r.Reason)
	}
}

func TestClaim_MentionOverride(t *testing.T) {
	tr := NewTracker(TrackerConfig{MentionTTL: time.Hour})
	tr.Claim("agent-a", "slack", "thread-1")
	tr.TrackMention("agent-b", "slack", "thread-1")
	r := tr.Claim("agent-b", "slack", "thread-1")
	if !r.Allowed {
		t.Fatal("mentioned agent should be allowed")
	}
	if r.Reason != "mention override" {
		t.Fatalf("Reason = %q, want mention override", r.Reason)
	}
	// Owner should still be agent-a.
	if r.Owner != "agent-a" {
		t.Fatalf("Owner = %q, want agent-a", r.Owner)
	}
}

func TestClaim_MentionExpired(t *testing.T) {
	tr := NewTracker(TrackerConfig{MentionTTL: 1 * time.Millisecond})
	tr.Claim("agent-a", "slack", "thread-1")
	tr.TrackMention("agent-b", "slack", "thread-1")

	// Wait for mention to expire.
	time.Sleep(5 * time.Millisecond)

	r := tr.Claim("agent-b", "slack", "thread-1")
	if r.Allowed {
		t.Fatal("expired mention should not override")
	}
}

func TestClaim_EnforcedChannelsScoping(t *testing.T) {
	tr := NewTracker(TrackerConfig{
		EnforcedChannels: map[string]bool{"slack": true},
	})
	tr.Claim("agent-a", "slack", "thread-1")

	// Different channel not enforced — should be allowed.
	r := tr.Claim("agent-b", "discord", "thread-1")
	if !r.Allowed {
		t.Fatal("unenforced channel should allow any agent")
	}
	if r.Reason != "channel not enforced" {
		t.Fatalf("Reason = %q", r.Reason)
	}

	// Enforced channel — should be blocked.
	r = tr.Claim("agent-b", "slack", "thread-1")
	if r.Allowed {
		t.Fatal("enforced channel should block other agents")
	}
}

func TestClaim_OwnerTTL(t *testing.T) {
	tr := NewTracker(TrackerConfig{OwnerTTL: 1 * time.Millisecond})
	tr.Claim("agent-a", "slack", "thread-1")

	// Wait for ownership to expire.
	time.Sleep(5 * time.Millisecond)

	// Now agent-b should be able to claim.
	r := tr.Claim("agent-b", "slack", "thread-1")
	if !r.Allowed || r.Owner != "agent-b" || r.Reason != "claimed" {
		t.Fatalf("expired ownership should allow new claim: %+v", r)
	}
}

func TestClaim_DifferentThreadsIndependent(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	tr.Claim("agent-a", "slack", "thread-1")
	r := tr.Claim("agent-b", "slack", "thread-2")
	if !r.Allowed || r.Owner != "agent-b" {
		t.Fatal("different threads should be independent")
	}
}

func TestClaim_DifferentChannelsSameThread(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	tr.Claim("agent-a", "slack", "thread-1")
	r := tr.Claim("agent-b", "discord", "thread-1")
	if !r.Allowed || r.Owner != "agent-b" {
		t.Fatal("same threadID in different channels should be independent")
	}
}

// ── ShouldSend ──────────────────────────────────────────────────────────────

func TestShouldSend_Convenience(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	if !tr.ShouldSend("agent-a", "slack", "") {
		t.Fatal("top-level should be allowed")
	}
	tr.Claim("agent-a", "slack", "thread-1")
	if tr.ShouldSend("agent-b", "slack", "thread-1") {
		t.Fatal("should be blocked")
	}
	if !tr.ShouldSend("agent-a", "slack", "thread-1") {
		t.Fatal("owner should be allowed")
	}
}

// ── TrackMention / IsMentioned ──────────────────────────────────────────────

func TestTrackMention_EmptyInputsIgnored(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	tr.TrackMention("", "slack", "thread-1")
	tr.TrackMention("agent", "slack", "")
	stats := tr.Stats()
	if stats.TotalMentions != 0 {
		t.Fatalf("expected 0 mentions for empty inputs, got %d", stats.TotalMentions)
	}
}

func TestIsMentioned_Basic(t *testing.T) {
	tr := NewTracker(TrackerConfig{MentionTTL: time.Hour})
	tr.TrackMention("agent-b", "slack", "thread-1")

	if !tr.IsMentioned("agent-b", "slack", "thread-1") {
		t.Fatal("should be mentioned")
	}
	if tr.IsMentioned("agent-a", "slack", "thread-1") {
		t.Fatal("unmentioned agent should not be detected")
	}
	if tr.IsMentioned("agent-b", "slack", "") {
		t.Fatal("empty threadID should return false")
	}
}

func TestIsMentioned_Expiry(t *testing.T) {
	tr := NewTracker(TrackerConfig{MentionTTL: 1 * time.Millisecond})
	tr.TrackMention("agent-b", "slack", "thread-1")
	time.Sleep(5 * time.Millisecond)
	if tr.IsMentioned("agent-b", "slack", "thread-1") {
		t.Fatal("expired mention should return false")
	}
}

// ── Owner ───────────────────────────────────────────────────────────────────

func TestOwner_Unclaimed(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	if owner := tr.Owner("slack", "thread-1"); owner != "" {
		t.Fatalf("unclaimed thread owner = %q, want empty", owner)
	}
}

func TestOwner_EmptyThread(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	if owner := tr.Owner("slack", ""); owner != "" {
		t.Fatalf("empty threadID owner = %q, want empty", owner)
	}
}

func TestOwner_Claimed(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	tr.Claim("agent-a", "slack", "thread-1")
	if owner := tr.Owner("slack", "thread-1"); owner != "agent-a" {
		t.Fatalf("owner = %q, want agent-a", owner)
	}
}

func TestOwner_Expired(t *testing.T) {
	tr := NewTracker(TrackerConfig{OwnerTTL: 1 * time.Millisecond})
	tr.Claim("agent-a", "slack", "thread-1")
	time.Sleep(5 * time.Millisecond)
	if owner := tr.Owner("slack", "thread-1"); owner != "" {
		t.Fatalf("expired owner = %q, want empty", owner)
	}
}

// ── Reset ───────────────────────────────────────────────────────────────────

func TestReset(t *testing.T) {
	tr := NewTracker(TrackerConfig{})
	tr.Claim("agent-a", "slack", "thread-1")
	tr.TrackMention("agent-b", "slack", "thread-1")
	tr.Reset()
	stats := tr.Stats()
	if stats.TotalOwners != 0 || stats.TotalMentions != 0 {
		t.Fatalf("after reset: owners=%d mentions=%d", stats.TotalOwners, stats.TotalMentions)
	}
}

// ── Stats ───────────────────────────────────────────────────────────────────

func TestStats(t *testing.T) {
	tr := NewTracker(TrackerConfig{MentionTTL: time.Hour})
	tr.Claim("agent-a", "slack", "thread-1")
	tr.Claim("agent-b", "slack", "thread-2")
	tr.TrackMention("agent-c", "slack", "thread-1")

	stats := tr.Stats()
	if stats.ActiveOwners != 2 {
		t.Fatalf("ActiveOwners = %d, want 2", stats.ActiveOwners)
	}
	if stats.ActiveMentions != 1 {
		t.Fatalf("ActiveMentions = %d, want 1", stats.ActiveMentions)
	}
	if stats.TotalOwners != 2 {
		t.Fatalf("TotalOwners = %d, want 2", stats.TotalOwners)
	}
	if stats.TotalMentions != 1 {
		t.Fatalf("TotalMentions = %d, want 1", stats.TotalMentions)
	}
}

// ── DetectMention ───────────────────────────────────────────────────────────

func TestDetectMention_AgentName(t *testing.T) {
	if !DetectMention("Hey @TestBot help me", "TestBot") {
		t.Fatal("should detect @TestBot")
	}
	if !DetectMention("hey @testbot help me", "TestBot") {
		t.Fatal("should be case-insensitive")
	}
	if DetectMention("hey testbot help me", "TestBot") {
		t.Fatal("should require @ prefix")
	}
}

func TestDetectMention_SlackStyle(t *testing.T) {
	if !DetectMention("Hey <@U999> help", "", "U999") {
		t.Fatal("should detect <@U999>")
	}
}

func TestDetectMention_BareAlias(t *testing.T) {
	if !DetectMention("asking npub1abc123 for help", "", "npub1abc123") {
		t.Fatal("should detect bare npub alias")
	}
}

func TestDetectMention_Empty(t *testing.T) {
	if DetectMention("", "TestBot") {
		t.Fatal("empty message should not match")
	}
	if DetectMention("hello", "") {
		t.Fatal("empty agent name with no aliases should not match")
	}
}

func TestDetectMention_EmptyAlias(t *testing.T) {
	if DetectMention("hello world", "", "", " ") {
		t.Fatal("empty aliases should be skipped")
	}
}

func TestDetectMention_MultipleAliases(t *testing.T) {
	if !DetectMention("cc @helper", "MainBot", "helper") {
		t.Fatal("should match alias @helper")
	}
	if !DetectMention("hey @MainBot", "MainBot", "helper") {
		t.Fatal("should match agent name")
	}
	if DetectMention("hello world", "MainBot", "helper") {
		t.Fatal("should not match unrelated text")
	}
}

// ── Concurrency ─────────────────────────────────────────────────────────────

func TestTracker_ConcurrentAccess(t *testing.T) {
	tr := NewTracker(TrackerConfig{MentionTTL: time.Hour})
	var wg sync.WaitGroup

	// Multiple goroutines claiming different threads.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			agentID := "agent-a"
			if n%2 == 0 {
				agentID = "agent-b"
			}
			tr.Claim(agentID, "slack", "thread-shared")
			tr.TrackMention(agentID, "slack", "thread-shared")
			tr.IsMentioned(agentID, "slack", "thread-shared")
			tr.Owner("slack", "thread-shared")
			tr.Stats()
		}(i)
	}
	wg.Wait()

	// Should not have panicked.
	stats := tr.Stats()
	if stats.TotalOwners < 1 {
		t.Fatal("expected at least 1 owner after concurrent access")
	}
}

// ── SweepExpired ────────────────────────────────────────────────────────────

func TestSweepExpired_CleansOwnersAndMentions(t *testing.T) {
	tr := NewTracker(TrackerConfig{
		OwnerTTL:   1 * time.Millisecond,
		MentionTTL: 1 * time.Millisecond,
	})
	tr.Claim("agent-a", "slack", "thread-1")
	tr.TrackMention("agent-b", "slack", "thread-1")

	time.Sleep(5 * time.Millisecond)

	// Next claim triggers sweep.
	tr.Claim("agent-c", "slack", "thread-2")
	stats := tr.Stats()
	// thread-1 owner should have been swept, thread-2 is new.
	if stats.TotalOwners != 1 {
		t.Fatalf("TotalOwners = %d after sweep, want 1", stats.TotalOwners)
	}
	// Mentions should have been swept.
	if stats.TotalMentions != 0 {
		t.Fatalf("TotalMentions = %d after sweep, want 0", stats.TotalMentions)
	}
}
