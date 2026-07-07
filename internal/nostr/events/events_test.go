package events

import (
	"encoding/json"
	"testing"
	"time"
)

// ─── Tag constants ────────────────────────────────────────────────────────────

func TestTagConstants_NonEmpty(t *testing.T) {
	// Canonical tags only (excludes deprecated aliases)
	tags := map[string]string{
		// Standard NIP Tags
		"TagEventRef":   TagEventRef,
		"TagPubkey":     TagPubkey,
		"TagDTag":       TagDTag,
		"TagKind":       TagKind,
		"TagExpiration": TagExpiration,

		// Cascadia Routing Tags
		"TagDomain":      TagDomain,
		"TagOp":          TagOp,
		"TagService":     TagService,
		"TagEnvironment": TagEnvironment,
		"TagWorker":      TagWorker,
		"TagAgent":       TagAgent,
		"TagRoute":       TagRoute,
		"TagRelease":     TagRelease,

		// Cascadia Correlation Tags
		"TagSession": TagSession,
		"TagRun":     TagRun,
		"TagIntent":  TagIntent,

		// Cascadia Lineage Tags
		"TagSchema":     TagSchema,
		"TagType":       TagType,
		"TagArtifact":   TagArtifact,
		"TagProject":    TagProject,
		"TagWorkflow":   TagWorkflow,
		"TagVersion":    TagVersion,
		"TagSupersedes": TagSupersedes,

		// Cascadia State Tags
		"TagStatus":   TagStatus,
		"TagStage":    TagStage,
		"TagStep":     TagStep,
		"TagDecision": TagDecision,

		// Cascadia Discovery Tags
		"TagCap":               TagCap,
		"TagRuntime":           TagRuntime,
		"TagModel":             TagModel,
		"TagTools":             TagTools,
		"TagTool":              TagTool,
		"TagBackend":           TagBackend,
		"TagScope":             TagScope,
		"TagDMSchemes":         TagDMSchemes,
		"TagACPVersion":        TagACPVersion,
		"TagContextVMFeatures": TagContextVMFeatures,

		// Application Tags
		"TagClient":  TagClient,
		"TagRelay":   TagRelay,
		"TagTopic":   TagTopic,
		"TagKeyword": TagKeyword,
		"TagSource":  TagSource,
		"TagGoal":    TagGoal,
		"TagRef":     TagRef,
		"TagRole":    TagRole,

		// Memory Tags
		"TagMemType":   TagMemType,
		"TagMemTaskID": TagMemTaskID,
		"TagMemSource": TagMemSource,

		// Feedback/Proposal Tags
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
	// Canonical tags only - deprecated aliases intentionally duplicate canonical values
	tags := []string{
		// Standard NIP Tags
		TagEventRef, TagPubkey, TagDTag, TagKind, TagExpiration,

		// Cascadia Routing Tags
		TagDomain, TagOp, TagService, TagEnvironment, TagWorker,
		TagAgent, TagRoute, TagRelease,

		// Cascadia Correlation Tags
		TagSession, TagRun, TagIntent,

		// Cascadia Lineage Tags
		TagSchema, TagType, TagArtifact, TagProject, TagWorkflow,
		TagVersion, TagSupersedes,

		// Cascadia State Tags
		TagStatus, TagStage, TagStep, TagDecision,

		// Cascadia Discovery Tags
		TagCap, TagRuntime, TagModel, TagTools, TagTool,
		TagBackend, TagScope, TagDMSchemes, TagACPVersion, TagContextVMFeatures,

		// Application Tags
		TagClient, TagRelay, TagTopic, TagKeyword, TagSource, TagGoal, TagRef, TagRole,

		// Memory Tags
		TagMemType, TagMemTaskID, TagMemSource,

		// Feedback/Proposal Tags
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

func TestTagConstants_Aliases(t *testing.T) {
	// Verify deprecated aliases point to canonical constants
	tests := []struct {
		name      string
		alias     string
		canonical string
	}{
		{"TagRecipient -> TagPubkey", TagRecipient, TagPubkey},
		{"TagDedupe -> TagDTag", TagDedupe, TagDTag},
		{"TagRunID -> TagRun", TagRunID, TagRun},
	}
	for _, tt := range tests {
		if tt.alias != tt.canonical {
			t.Errorf("%s: alias %q != canonical %q", tt.name, tt.alias, tt.canonical)
		}
	}
}

// ─── Kind constants ───────────────────────────────────────────────────────────

func TestKindConstants_Values(t *testing.T) {
	tests := []struct {
		name string
		kind Kind
		want int
	}{
		// Standard NIP Kinds
		{"DM NIP-04", KindDMNIP04, 4},
		{"Seal", KindSeal, 13},
		{"DM NIP-44", KindDMNIP44, 44},
		{"GiftWrap", KindGiftWrap, 1059},

		// Cascadia Canonical Kinds
		{"CAS_AUDIT", CAS_AUDIT, 25910},
		{"CAS_WORKER_AD", CAS_WORKER_AD, 10100},
		{"CAS_AGENT_HEARTBEAT", CAS_AGENT_HEARTBEAT, 30316},
		{"CAS_AGENT_CAPABILITY", CAS_AGENT_CAPABILITY, 30317},
		{"CAS_CP_STATE", CAS_CP_STATE, 30900},

		// NIP-38 User Status
		{"NIP38Status", KindNIP38Status, 30315},

		// NIP-78 App-Specific Data
		{"AppData", KindAppData, 30078},

		// ContextVM Kinds
		{"ContextVM", KindContextVM, 25910},
		{"ContextVMServerAnnouncement", KindContextVMServerAnnouncement, 11316},
		{"ContextVMToolsList", KindContextVMToolsList, 11317},
		{"ContextVMResourcesList", KindContextVMResourcesList, 11318},
		{"ContextVMResourceTemplatesList", KindContextVMResourceTemplatesList, 11319},
		{"ContextVMPromptsList", KindContextVMPromptsList, 11320},

		// NIP-60 Cashu Wallet
		{"NIP60UnspentToken", KindNIP60UnspentToken, 7375},
		{"NIP60TokenHistory", KindNIP60TokenHistory, 7376},
		{"NIP60Wallet", KindNIP60Wallet, 17375},

		// NIP-61 Nutzap
		{"NIP61NutzapInfo", KindNIP61NutzapInfo, 10019},
		{"NIP61Nutzap", KindNIP61Nutzap, 9321},

		// NIP-58 Badges
		{"NIP58BadgeAward", KindNIP58BadgeAward, 8},
		{"NIP58ProfileBadges", KindNIP58ProfileBadges, 10008},
		{"NIP58BadgeSet", KindNIP58BadgeSet, 30008},
		{"NIP58BadgeDefinition", KindNIP58BadgeDefinition, 30009},

		// NIP-34 Repository Collaboration
		{"RepoAnnouncement", KindRepoAnnouncement, 30617},
		{"RepoState", KindRepoState, 30618},
		{"Patch", KindPatch, 1617},
		{"PR", KindPR, 1618},
		{"PRUpdate", KindPRUpdate, 1619},
		{"Issue", KindIssue, 1621},
		{"StatusOpen", KindStatusOpen, 1630},
		{"StatusApplied", KindStatusApplied, 1631},
		{"StatusClosed", KindStatusClosed, 1632},
		{"StatusDraft", KindStatusDraft, 1633},
	}
	for _, tt := range tests {
		if int(tt.kind) != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, tt.kind, tt.want)
		}
	}
}

func TestKindConstants_Unique(t *testing.T) {
	// Canonical kinds only - excludes deprecated aliases that intentionally share values
	kinds := []Kind{
		// Standard NIP Kinds
		KindDMNIP04, KindSeal, KindDMNIP44, KindGiftWrap,

		// Cascadia Canonical Kinds
		CAS_WORKER_AD, CAS_AGENT_HEARTBEAT, CAS_AGENT_CAPABILITY, CAS_CP_STATE,

		// NIP-38/NIP-78
		KindNIP38Status, KindAppData,

		// ContextVM
		KindContextVM, KindContextVMServerAnnouncement, KindContextVMToolsList,
		KindContextVMResourcesList, KindContextVMResourceTemplatesList, KindContextVMPromptsList,

		// NIP-60
		KindNIP60UnspentToken, KindNIP60TokenHistory, KindNIP60Wallet,

		// NIP-61
		KindNIP61NutzapInfo, KindNIP61Nutzap,

		// NIP-34
		KindRepoAnnouncement, KindRepoState, KindPatch, KindPR, KindPRUpdate,
		KindIssue, KindStatusOpen, KindStatusApplied, KindStatusClosed, KindStatusDraft,
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
