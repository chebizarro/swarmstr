package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/nostr/events"
)

func hasTag(tags nostr.Tags, key string, values ...string) bool {
	for _, tag := range tags {
		if len(tag) < 1 || tag[0] != key {
			continue
		}
		if len(values) == 0 {
			return true
		}
		if len(tag) < len(values)+1 {
			continue
		}
		ok := true
		for i, value := range values {
			if tag[i+1] != value {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestGeneratedCascadiaKindConstants(t *testing.T) {
	if events.CAS_AUDIT != 4903 || events.CAS_WORKER_AD != 10100 || events.KindContextVM != 25910 || events.CAS_AGENT_HEARTBEAT != 30316 || events.CAS_AGENT_CAPABILITY != 30317 {
		t.Fatalf("unexpected standard kinds: audit=%d worker=%d intent=%d heartbeat=%d capability=%d", events.CAS_AUDIT, events.CAS_WORKER_AD, events.KindContextVM, events.CAS_AGENT_HEARTBEAT, events.CAS_AGENT_CAPABILITY)
	}
}

func TestBuildWorkerAdvertisementTagsAndContent(t *testing.T) {
	ad := WorkerAdvertisement{
		AgentID:           "metiq-main",
		RuntimeVersion:    "test",
		Capabilities:      []string{"coding", "coding"},
		Tools:             []string{"memory_search"},
		ContextVMFeatures: []string{"agent/get-status"},
		Relays:            []string{"wss://relay.example"},
	}
	tags := BuildWorkerAdvertisementTags(ad)
	if !hasTag(tags, "status", "available") || !hasTag(tags, "agent", "metiq-main") || !hasTag(tags, "cap", "coding") || !hasTag(tags, "runtime", "metiq", "test") {
		t.Fatalf("worker ad tags missing standard fields: %#v", tags)
	}
	if !hasTag(tags, "session_mode", "fleet") {
		t.Fatalf("worker ad missing fleet session mode: %#v", tags)
	}
	var content WorkerAdvertisement
	if err := json.Unmarshal([]byte(BuildWorkerAdvertisementContent(ad)), &content); err != nil {
		t.Fatalf("worker ad content JSON: %v", err)
	}
	if content.Runtime != "metiq" || content.Status != "available" || len(content.Capabilities) != 1 {
		t.Fatalf("unexpected worker ad content: %#v", content)
	}
}

func TestAgentSessionModeExclusivity(t *testing.T) {
	if !AgentSessionModeFleet.AllowsFleetPublish() || AgentSessionModeFleet.AllowsPrivateCorpus() {
		t.Fatal("fleet mode must allow fleet publish and deny private corpus")
	}
	if AgentSessionModePrivate.AllowsFleetPublish() || !AgentSessionModePrivate.AllowsPrivateCorpus() {
		t.Fatal("private mode must deny fleet publish and allow private corpus")
	}
	// Unknown and empty values fail closed for private-corpus access while
	// preserving the existing fleet-mode default.
	if AgentSessionMode("unknown").AllowsPrivateCorpus() || !AgentSessionMode("").AllowsFleetPublish() {
		t.Fatal("invalid/default mode normalization is unsafe")
	}
}

func TestPrivateSessionBlocksCanonicalFleetPublishes(t *testing.T) {
	_, err := PublishWorkerAdvertisement(context.Background(), nil, nil, nil, WorkerAdvertisement{SessionMode: AgentSessionModePrivate})
	if err == nil || !strings.Contains(err.Error(), "private session") {
		t.Fatalf("worker publish error = %v", err)
	}
	_, err = PublishAuditRecord(context.Background(), nil, nil, nil, AuditRecord{SessionMode: AgentSessionModePrivate})
	if err == nil || !strings.Contains(err.Error(), "private session") {
		t.Fatalf("audit publish error = %v", err)
	}
}

func TestBuildAuditTagsAndContent(t *testing.T) {
	record := AuditRecord{Actor: "controller", Action: "agent/provision", Target: "agent:metiq-main", Outcome: "SUCCESS", Correlation: "req-1", AgentID: "metiq-main"}
	tags := BuildAuditTags(record)
	for _, want := range []struct{ key, value string }{
		{"domain", "agent-runtime"},
		{"type", "CAS_AUDIT"},
		{"schema", "cascadia.audit.v1"},
		{"actor", "controller"},
		{"action", "agent/provision"},
		{"target", "agent:metiq-main"},
		{"outcome", "success"},
		{"correlation", "req-1"},
		{"session_mode", "fleet"},
	} {
		if !hasTag(tags, want.key, want.value) {
			t.Fatalf("missing audit tag %s=%s in %#v", want.key, want.value, tags)
		}
	}
	var content AuditRecord
	if err := json.Unmarshal([]byte(BuildAuditContent(record)), &content); err != nil {
		t.Fatalf("audit content JSON: %v", err)
	}
	if content.Outcome != "success" || content.Action != "agent/provision" {
		t.Fatalf("unexpected audit content: %#v", content)
	}
}
