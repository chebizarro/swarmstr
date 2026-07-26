package methods

import (
	"encoding/json"
	"testing"
)

func TestDecodeBoardUpdateParams(t *testing.T) {
	req, err := DecodeBoardUpdateParams(json.RawMessage(`{"sessionKey":"sess","ops":[{"kind":"tab_create","tabId":"main","title":"Main"},{"kind":"widget_resize","name":"w","sizeW":4,"sizeH":3}]}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.SessionKey != "sess" || len(req.Ops) != 2 || req.Ops[0].TabID != "main" || req.Ops[1].SizeW != 4 {
		t.Fatalf("unexpected: %+v", req)
	}
	// snake_case session_key is aliased to sessionKey.
	req, err = DecodeBoardUpdateParams(json.RawMessage(`{"session_key":"sess","ops":[]}`))
	if err != nil {
		t.Fatalf("decode alias: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.SessionKey != "sess" {
		t.Fatalf("unexpected alias decode: %+v err=%v", req, err)
	}
	if req, _ := DecodeBoardUpdateParams(json.RawMessage(`{"ops":[]}`)); func() bool { _, err := req.Normalize(); return err == nil }() {
		t.Fatal("expected missing sessionKey error")
	}
}

func TestDecodeBoardWidgetPutParamsRejectsDeferredSources(t *testing.T) {
	req, err := DecodeBoardWidgetPutParams(json.RawMessage(`{"sessionKey":"sess","name":"w","content":{"kind":"html","html":"<p></p>"},"placement":{"tabId":"main","size":"lg"},"declared":{"netOrigins":["https://x"],"tools":["prompt"]}}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.Content.Kind != "html" || req.Placement.Size != "lg" || len(req.Declared.Tools) != 1 {
		t.Fatalf("unexpected: %+v", req)
	}
	for _, kind := range []string{"bogus"} {
		req, err := DecodeBoardWidgetPutParams(json.RawMessage(`{"sessionKey":"sess","name":"w","content":{"kind":"` + kind + `","viewId":"v","docId":"d"}}`))
		if err != nil {
			t.Fatalf("decode %s: %v", kind, err)
		}
		if _, err := req.Normalize(); err == nil {
			t.Fatalf("expected rejection for content kind %s", kind)
		}
	}
	// canvas-doc is an accepted source (swarmstr-5p0v item 1) but requires a docId.
	docReq, err := DecodeBoardWidgetPutParams(json.RawMessage(`{"sessionKey":"sess","name":"w","content":{"kind":"canvas-doc","docId":"doc-1"}}`))
	if err != nil {
		t.Fatalf("decode canvas-doc: %v", err)
	}
	if _, err := docReq.Normalize(); err != nil {
		t.Fatalf("canvas-doc with docId rejected: %v", err)
	}
	noDoc, err := DecodeBoardWidgetPutParams(json.RawMessage(`{"sessionKey":"sess","name":"w","content":{"kind":"canvas-doc"}}`))
	if err != nil {
		t.Fatalf("decode canvas-doc no docId: %v", err)
	}
	if _, err := noDoc.Normalize(); err == nil {
		t.Fatalf("expected canvas-doc without docId to be rejected")
	}
	// mcp-app is an accepted source but requires the originating viewId.
	appReq, err := DecodeBoardWidgetPutParams(json.RawMessage(`{"sessionKey":"sess","name":"w","content":{"kind":"mcp-app","viewId":"view_1"}}`))
	if err != nil {
		t.Fatalf("decode mcp-app: %v", err)
	}
	if _, err := appReq.Normalize(); err != nil {
		t.Fatalf("mcp-app content must be accepted: %v", err)
	}
	appReq, err = DecodeBoardWidgetPutParams(json.RawMessage(`{"sessionKey":"sess","name":"w","content":{"kind":"mcp-app"}}`))
	if err != nil {
		t.Fatalf("decode mcp-app without viewId: %v", err)
	}
	if _, err := appReq.Normalize(); err == nil {
		t.Fatal("expected viewId requirement for mcp-app content")
	}
}

func TestDecodeBoardWidgetGrantParams(t *testing.T) {
	// camelCase instanceId is aliased to instance_id.
	req, err := DecodeBoardWidgetGrantParams(json.RawMessage(`{"sessionKey":"sess","name":"w","decision":"granted","revision":2,"instanceId":"abc"}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if req.InstanceID != "abc" || req.Revision != 2 || req.Decision != "granted" {
		t.Fatalf("unexpected: %+v", req)
	}
	req, _ = DecodeBoardWidgetGrantParams(json.RawMessage(`{"sessionKey":"sess","name":"w","decision":"maybe","revision":2,"instanceId":"abc"}`))
	if _, err := req.Normalize(); err == nil {
		t.Fatal("expected decision validation error")
	}
}

func TestDecodeBoardEventParamsVariants(t *testing.T) {
	req, err := DecodeBoardEventParams(json.RawMessage(`{"sessionKey":"sess","widget":"chart","payload":{"a":1}}`))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.Widget != "chart" || string(req.Payload) != `{"a":1}` {
		t.Fatalf("unexpected: %+v err=%v", req, err)
	}
	req, err = DecodeBoardEventParams(json.RawMessage(`{"ticket":"tok","payload":{}}`))
	if err != nil {
		t.Fatalf("decode ticket variant: %v", err)
	}
	if req, err = req.Normalize(); err != nil || req.Ticket != "tok" {
		t.Fatalf("ticket variant must be accepted: %+v err=%v", req, err)
	}
	// The two identity variants are mutually exclusive.
	req, err = DecodeBoardEventParams(json.RawMessage(`{"ticket":"tok","sessionKey":"sess","widget":"chart","payload":{}}`))
	if err != nil {
		t.Fatalf("decode mixed variant: %v", err)
	}
	if _, err := req.Normalize(); err == nil {
		t.Fatal("expected mutual-exclusion rejection")
	}
}

func TestDecodeConversationsParams(t *testing.T) {
	list, err := DecodeConversationsListParams(json.RawMessage(`{"agentId":"main","channel":"telegram","query":"@bob","limit":10}`))
	if err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list, err = list.Normalize(); err != nil || list.AgentID != "main" || list.Limit != 10 {
		t.Fatalf("unexpected list: %+v err=%v", list, err)
	}
	list, _ = DecodeConversationsListParams(json.RawMessage(`{"agentId":"main"}`))
	if list, err = list.Normalize(); err != nil || list.Limit != 50 {
		t.Fatalf("expected default limit: %+v err=%v", list, err)
	}

	send, err := DecodeConversationsSendParams(json.RawMessage(`{"agentId":"main","operationId":"op-1","conversationRef":"conv_abc","message":"hi","sourceSessionKey":"sess"}`))
	if err != nil {
		t.Fatalf("decode send: %v", err)
	}
	if send, err = send.Normalize(); err != nil || send.OperationID != "op-1" || send.SourceSessionKey != "sess" {
		t.Fatalf("unexpected send: %+v err=%v", send, err)
	}
	send, _ = DecodeConversationsSendParams(json.RawMessage(`{"agentId":"main","operationId":"op-1","conversationRef":"conv_abc","message":"  "}`))
	if _, err := send.Normalize(); err == nil {
		t.Fatal("expected empty message error")
	}

	// camelCase timeoutMs is aliased to timeout_ms.
	turn, err := DecodeConversationsTurnParams(json.RawMessage(`{"agentId":"main","turnId":"t-1","conversationRef":"conv_abc","message":"ping","timeoutMs":1500}`))
	if err != nil {
		t.Fatalf("decode turn: %v", err)
	}
	if turn, err = turn.Normalize(); err != nil || turn.TimeoutMS != 1500 {
		t.Fatalf("unexpected turn: %+v err=%v", turn, err)
	}
	turn, _ = DecodeConversationsTurnParams(json.RawMessage(`{"agentId":"main","turnId":"t-1","conversationRef":"conv_abc","message":"ping","timeoutMs":900000}`))
	if _, err := turn.Normalize(); err == nil {
		t.Fatal("expected timeout range error")
	}

	cancel, err := DecodeConversationsTurnCancelParams(json.RawMessage(`{"agentId":"main","turnId":"t-1"}`))
	if err != nil {
		t.Fatalf("decode cancel: %v", err)
	}
	if cancel, err = cancel.Normalize(); err != nil || cancel.TurnID != "t-1" {
		t.Fatalf("unexpected cancel: %+v err=%v", cancel, err)
	}
}

func TestBoardConversationMethodsRegisteredWithScopes(t *testing.T) {
	supported := map[string]struct{}{}
	for _, m := range SupportedMethods() {
		supported[m] = struct{}{}
	}
	wantScopes := map[string]string{
		MethodBoardGet:                "operator.read",
		MethodBoardUpdate:             "operator.write",
		MethodBoardWidgetPut:          "operator.write",
		MethodBoardWidgetGrant:        "operator.approvals",
		MethodBoardEvent:              "operator.write",
		MethodConversationsList:       "operator.admin",
		MethodConversationsSend:       "operator.admin",
		MethodConversationsTurn:       "operator.admin",
		MethodConversationsTurnCancel: "operator.admin",
	}
	for method, scope := range wantScopes {
		if _, ok := supported[method]; !ok {
			t.Fatalf("method not registered: %s", method)
		}
		if got := MethodDescriptor(method).Scope; got != scope {
			t.Fatalf("scope mismatch for %s: got %s want %s", method, got, scope)
		}
		if !InAdminDispatchGroup(AdminDispatchWorkspace, method) {
			t.Fatalf("method not in workspace dispatch group: %s", method)
		}
	}
}
