package methods

import (
	"encoding/json"
	"testing"
)

func TestSessionLifecycleAliasDecoders(t *testing.T) {
	send, err := DecodeChatSendParams(json.RawMessage(`{"key":"session-a","agentId":"main","message":"hello","timeoutMs":1000}`))
	if err != nil {
		t.Fatal(err)
	}
	send, err = send.Normalize()
	if err != nil || send.To != "session-a" || send.Text != "hello" {
		t.Fatalf("send=%+v err=%v", send, err)
	}

	describe, err := DecodeSessionGetParams(json.RawMessage(`{"key":"session-a","includeDerivedTitles":true,"includeLastMessage":true}`))
	if err != nil {
		t.Fatal(err)
	}
	describe, err = describe.Normalize()
	if err != nil || describe.SessionID != "session-a" {
		t.Fatalf("describe=%+v err=%v", describe, err)
	}

	abort, err := DecodeChatAbortParams(json.RawMessage(`{"key":"session-a","runId":"run-a","agentId":"main"}`))
	if err != nil {
		t.Fatal(err)
	}
	abort, err = abort.Normalize()
	if err != nil || abort.SessionID != "session-a" || abort.RunID != "run-a" {
		t.Fatalf("abort=%+v err=%v", abort, err)
	}
}

func TestSessionsCreateSchemaValidation(t *testing.T) {
	req, err := DecodeSessionsCreateParams(json.RawMessage(`{"key":"session-a","agentId":"main","label":"Demo","visibility":"shared","model":"test","thinkingLevel":"low"}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err = req.Normalize()
	if err != nil || req.Key != "session-a" || req.AgentID != "main" {
		t.Fatalf("request=%+v err=%v", req, err)
	}
	bad, err := DecodeSessionsCreateParams(json.RawMessage(`{"fork":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Normalize(); err == nil {
		t.Fatal("expected lineage validation error")
	}
}

func TestSupportedMethodsIncludesSessionLifecycleAliases(t *testing.T) {
	set := map[string]bool{}
	for _, method := range SupportedMethods() {
		set[method] = true
	}
	for _, method := range []string{MethodSessionsMessagesSubscribe, MethodSessionsMessagesUnsubscribe, MethodSessionsDescribe, MethodSessionsCreate, MethodSessionsSend, MethodSessionsAbort} {
		if !set[method] {
			t.Fatalf("supported methods missing %s", method)
		}
	}
}
