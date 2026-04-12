package events

import (
	"encoding/json"
	"testing"
	"time"
)

// ─── Tag constants ────────────────────────────────────────────────────────────

func TestTagConstants_NonEmpty(t *testing.T) {
	tags := map[string]string{
		"TagTaskID":           TagTaskID,
		"TagAgent":            TagAgent,
		"TagStage":            TagStage,
		"TagRecipient":        TagRecipient,
		"TagDedupe":           TagDedupe,
		"TagRef":              TagRef,
		"TagSession":          TagSession,
		"TagKind":             TagKind,
		"TagRole":             TagRole,
		"TagTopic":            TagTopic,
		"TagKeyword":          TagKeyword,
		"TagSource":           TagSource,
		"TagGoal":             TagGoal,
		"TagRunID":            TagRunID,
		"TagMemType":          TagMemType,
		"TagMemTaskID":        TagMemTaskID,
		"TagMemSource":        TagMemSource,
		"TagFeedback":         TagFeedback,
		"TagFeedbackSource":   TagFeedbackSource,
		"TagFeedbackSeverity": TagFeedbackSeverity,
		"TagFeedbackCategory": TagFeedbackCategory,
		"TagStepID":           TagStepID,
		"TagProposal":         TagProposal,
		"TagProposalKind":     TagProposalKind,
		"TagProposalStatus":   TagProposalStatus,
		"TagRetro":            TagRetro,
		"TagRetroTrigger":     TagRetroTrigger,
		"TagRetroOutcome":     TagRetroOutcome,
	}
	for name, val := range tags {
		if val == "" {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestTagConstants_Unique(t *testing.T) {
	tags := []string{
		TagTaskID, TagAgent, TagStage, TagRecipient, TagDedupe, TagRef,
		TagSession, TagKind, TagRole, TagTopic, TagKeyword, TagSource,
		TagGoal, TagRunID, TagMemType, TagMemTaskID, TagMemSource,
		TagFeedback, TagFeedbackSource, TagFeedbackSeverity, TagFeedbackCategory,
		TagStepID, TagProposal, TagProposalKind, TagProposalStatus,
		TagRetro, TagRetroTrigger, TagRetroOutcome,
	}
	seen := make(map[string]bool, len(tags))
	for _, v := range tags {
		if seen[v] {
			t.Errorf("duplicate tag value: %q", v)
		}
		seen[v] = true
	}
}

// ─── Kind constants ───────────────────────────────────────────────────────────

func TestKindConstants_Values(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want int
	}{
		{"DM NIP-04", KindDMNIP04, 4},
		{"Seal", KindSeal, 13},
		{"DM NIP-44", KindDMNIP44, 44},
		{"GiftWrap", KindGiftWrap, 1059},
		{"Task", KindTask, 38383},
		{"Control", KindControl, 38384},
		{"MCPCall", KindMCPCall, 38385},
		{"MCPResult", KindMCPResult, 38386},
		{"LogStatus", KindLogStatus, 30315},
		{"Lifecycle", KindLifecycle, 30316},
		{"Capability", KindCapability, 30317},
		{"NIP60UnspentToken", KindNIP60UnspentToken, 7375},
		{"NIP60TokenHistory", KindNIP60TokenHistory, 7376},
		{"NIP60Wallet", KindNIP60Wallet, 37375},
		{"NIP61NutzapInfo", KindNIP61NutzapInfo, 10019},
		{"NIP61Nutzap", KindNIP61Nutzap, 9321},
		{"StateDoc", KindStateDoc, 30078},
		{"TranscriptDoc", KindTranscriptDoc, 30079},
		{"MemoryDoc", KindMemoryDoc, 30080},
	}
	for _, tt := range tests {
		if int(tt.kind) != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, tt.kind, tt.want)
		}
	}
}

func TestKindConstants_Unique(t *testing.T) {
	kinds := []Kind{
		KindDMNIP04, KindSeal, KindDMNIP44, KindGiftWrap,
		KindTask, KindControl, KindMCPCall, KindMCPResult,
		KindLogStatus, KindLifecycle, KindCapability,
		KindNIP60UnspentToken, KindNIP60TokenHistory, KindNIP60Wallet,
		KindNIP61NutzapInfo, KindNIP61Nutzap,
		KindRepoAnnouncement, KindRepoState, KindPatch, KindPR, KindPRUpdate,
		KindIssue, KindStatusOpen, KindStatusApplied, KindStatusClosed, KindStatusDraft,
		KindStateDoc, KindTranscriptDoc, KindMemoryDoc,
	}
	seen := make(map[Kind]bool, len(kinds))
	for _, k := range kinds {
		if seen[k] {
			t.Errorf("duplicate kind: %d", k)
		}
		seen[k] = true
	}
}

// ─── Envelope ─────────────────────────────────────────────────────────────────

func TestNewEnvelope_Unencrypted(t *testing.T) {
	env := NewEnvelope("task_state", `{"id":"abc"}`, false)
	if env.Version != 1 {
		t.Errorf("version: got %d, want 1", env.Version)
	}
	if env.Type != "task_state" {
		t.Errorf("type: got %q", env.Type)
	}
	if env.Payload != `{"id":"abc"}` {
		t.Errorf("payload mismatch")
	}
	if env.Enc != "" {
		t.Errorf("enc should be empty for unencrypted, got %q", env.Enc)
	}
	ts, ok := env.Meta["created_at_unix"]
	if !ok {
		t.Fatal("missing created_at_unix in meta")
	}
	if tsInt, ok := ts.(int64); !ok || tsInt == 0 {
		t.Errorf("created_at_unix should be non-zero int64, got %v (%T)", ts, ts)
	}
}

func TestNewEnvelope_Encrypted(t *testing.T) {
	env := NewEnvelope("memory", "ciphertext", true)
	if env.Enc != "nip44" {
		t.Errorf("enc: got %q, want nip44", env.Enc)
	}
}

func TestNewEnvelopeWithEncoding(t *testing.T) {
	env := NewEnvelopeWithEncoding("custom", "data", "chacha20")
	if env.Enc != "chacha20" {
		t.Errorf("enc: got %q, want chacha20", env.Enc)
	}
	if env.Type != "custom" {
		t.Errorf("type: got %q", env.Type)
	}
}

func TestNewEnvelopeWithEncoding_EmptyEnc(t *testing.T) {
	env := NewEnvelopeWithEncoding("plain", "data", "")
	if env.Enc != "" {
		t.Errorf("enc should be empty, got %q", env.Enc)
	}
}

func TestEnvelope_JSONRoundTrip(t *testing.T) {
	orig := NewEnvelope("state", `{"key":"val"}`, true)
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Envelope
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != orig.Version || decoded.Type != orig.Type ||
		decoded.Payload != orig.Payload || decoded.Enc != orig.Enc {
		t.Errorf("round-trip mismatch: %+v vs %+v", orig, decoded)
	}
}

func TestEnvelope_MetaTimestamp_RecentEnough(t *testing.T) {
	before := time.Now().Unix()
	env := NewEnvelope("test", "", false)
	after := time.Now().Unix()
	ts := env.Meta["created_at_unix"].(int64)
	if ts < before || ts > after {
		t.Errorf("timestamp %d not in [%d, %d]", ts, before, after)
	}
}
