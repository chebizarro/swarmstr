package ws

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type payloadContract struct {
	Alias        string   `json:"alias"`
	RequiredKeys []string `json:"required_keys"`
}

type canonicalPayloadContract struct {
	RequiredKeys []string `json:"required_keys"`
}

func TestCompatibilityEventProjections_MatchFixtureContracts(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "openclaw_event_payload_contracts.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	contracts := map[string]payloadContract{}
	if err := json.Unmarshal(raw, &contracts); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	for sourceEvent, contract := range contracts {
		payload := samplePayloadForEvent(sourceEvent)
		if payload == nil {
			t.Fatalf("missing sample payload for %s", sourceEvent)
		}
		projections := compatibilityEventProjections(sourceEvent, payload)
		if len(projections) == 0 {
			t.Fatalf("no compatibility projections for %s", sourceEvent)
		}
		var matched any
		for _, p := range projections {
			if p.Event == contract.Alias {
				matched = p.Payload
				break
			}
		}
		if matched == nil {
			t.Fatalf("missing alias %q for source event %q", contract.Alias, sourceEvent)
		}
		obj, ok := matched.(map[string]any)
		if !ok {
			t.Fatalf("projection payload for %s is not object: %T", sourceEvent, matched)
		}
		for _, keyPath := range contract.RequiredKeys {
			if !hasPath(obj, keyPath) {
				t.Fatalf("projection payload for %s missing required key path %q: %#v", sourceEvent, keyPath, obj)
			}
		}
	}
}

func samplePayloadForEvent(event string) any {
	switch event {
	case EventAgentStatus:
		return AgentStatusPayload{TS: 1, AgentID: "main", Status: "thinking", Session: "sess-1"}
	case EventChatMessage:
		return ChatMessagePayload{TS: 2, SessionID: "sess-1", Direction: "outbound", Text: "hello"}
	case EventChatChunk:
		return ChatChunkPayload{TS: 3, SessionID: "sess-1", Text: "hel", Done: false}
	case EventCronTick:
		return CronTickPayload{TS: 4, JobID: "job-1"}
	case EventCronResult:
		return CronResultPayload{TS: 5, JobID: "job-1", Succeeded: true, DurationMS: 10}
	case EventTick:
		return TickPayload{TS: 6, UptimeMS: 100}
	case EventVoicewake:
		return VoicewakePayload{TS: 7, Trigger: "hey swarm"}
	default:
		return nil
	}
}

func TestCanonicalEventPayloads_MatchFixtureContracts(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "metiq_core_event_payload_contracts.json"))
	if err != nil {
		t.Fatalf("read canonical fixture: %v", err)
	}
	contracts := map[string]canonicalPayloadContract{}
	if err := json.Unmarshal(raw, &contracts); err != nil {
		t.Fatalf("parse canonical fixture: %v", err)
	}
	for event, contract := range contracts {
		sample := sampleCanonicalPayloadForEvent(event)
		if sample == nil {
			t.Fatalf("missing canonical sample payload for %s", event)
		}
		obj := toObject(t, sample)
		assertRequiredPaths(t, event, obj, contract.RequiredKeys)
	}
}

func TestConnectChallengeFrame_MatchesFixtureContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "connect_challenge_contract.json"))
	if err != nil {
		t.Fatalf("read connect challenge fixture: %v", err)
	}
	var fx canonicalPayloadContract
	if err := json.Unmarshal(raw, &fx); err != nil {
		t.Fatalf("parse connect challenge fixture: %v", err)
	}

	frame := map[string]any{
		"type":  "event",
		"event": "connect.challenge",
		"payload": map[string]any{
			"nonce": "nonce-1",
			"ts":    int64(123),
		},
	}
	assertRequiredPaths(t, "connect.challenge", frame, fx.RequiredKeys)
}

func sampleCanonicalPayloadForEvent(event string) any {
	switch event {
	case EventHealth:
		return HealthPayload{TS: 1, OK: true, Info: map[string]any{"source": "test"}}
	case EventTalkMode:
		return TalkModePayload{TS: 2, Mode: "push-to-talk"}
	case EventUpdateAvailable:
		return UpdateAvailablePayload{TS: 3, Version: "1.2.3", Source: "ota"}
	case EventExecApprovalRequested:
		return ExecApprovalRequestedPayload{TS: 4, ID: "req-1", NodeID: "node-1", Command: "ls"}
	case EventExecApprovalResolved:
		return ExecApprovalResolvedPayload{TS: 5, ID: "req-1", Decision: "approved", NodeID: "node-1"}
	case EventNodePairRequested:
		return NodePairRequestedPayload{TS: 6, RequestID: "pair-1", NodeID: "node-1", Label: "Node"}
	case EventNodePairResolved:
		return NodePairResolvedPayload{TS: 7, RequestID: "pair-1", NodeID: "node-1", Decision: "approved"}
	case EventDevicePairResolved:
		return DevicePairResolvedPayload{TS: 8, DeviceID: "dev-1", Label: "Phone", Decision: "approved"}
	case "presence.updated":
		return map[string]any{"presence": []map[string]any{{"host": "h1", "mode": "user", "ts": int64(9)}}}
	case EventChannelMessage:
		return ChannelMessagePayload{TS: 10, ChannelID: "ch-1", Direction: "inbound", Text: "hello"}
	case EventConfigUpdated:
		return ConfigUpdatedPayload{TS: 11}
	case EventPluginLoaded:
		return PluginLoadedPayload{TS: 12, PluginID: "p1", Action: "loaded"}
	case EventCanvasUpdate:
		return CanvasUpdatePayload{TS: 13, CanvasID: "c1", ContentType: "text/markdown", Data: "# test"}
	default:
		return nil
	}
}

func toObject(t *testing.T, v any) map[string]any {
	t.Helper()
	if m, ok := v.(map[string]any); ok {
		return m
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return out
}

func assertRequiredPaths(t *testing.T, event string, obj map[string]any, required []string) {
	t.Helper()
	for _, keyPath := range required {
		if !hasPath(obj, keyPath) {
			t.Fatalf("payload for %s missing required key path %q: %#v", event, keyPath, obj)
		}
	}
}

func hasPath(obj map[string]any, path string) bool {
	cur := any(obj)
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		next, ok := m[part]
		if !ok {
			return false
		}
		cur = next
	}
	return true
}
