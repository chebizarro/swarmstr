package methods

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"metiq/internal/store/state"
)

func TestDecodeMemorySearchParams_ObjectAndPositionalParity(t *testing.T) {
	objRaw := json.RawMessage(`{"query":"hello","limit":7}`)
	arrRaw := json.RawMessage(`["hello",7]`)

	a, err := DecodeMemorySearchParams(objRaw)
	if err != nil {
		t.Fatalf("object decode error: %v", err)
	}
	b, err := DecodeMemorySearchParams(arrRaw)
	if err != nil {
		t.Fatalf("array decode error: %v", err)
	}
	if a.Query != b.Query || a.Limit != b.Limit {
		t.Fatalf("parity mismatch object=%+v positional=%+v", a, b)
	}
}

func TestDecodeSessionGetParams_RejectFractionalLimit(t *testing.T) {
	_, err := DecodeSessionGetParams(json.RawMessage(`["session-1",1.5]`))
	if err == nil {
		t.Fatal("expected error for fractional positional limit")
	}
}

func TestAgentRequestNormalize_ParsesMemoryScope(t *testing.T) {
	req, err := (AgentRequest{
		SessionID:   " sess-1 ",
		Message:     " hello ",
		Context:     " extra ",
		MemoryScope: state.AgentMemoryScope("project"),
		TimeoutMS:   500,
	}).Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.SessionID != "sess-1" || req.Message != "hello" || req.Context != "extra" {
		t.Fatalf("unexpected normalized request: %#v", req)
	}
	if req.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("expected project memory scope, got %#v", req.MemoryScope)
	}
	if req.TimeoutMS != 500 {
		t.Fatalf("expected timeout to be preserved, got %d", req.TimeoutMS)
	}
}

func TestAgentRequestNormalize_RejectsInvalidMemoryScope(t *testing.T) {
	_, err := (AgentRequest{
		Message:     "hello",
		MemoryScope: state.AgentMemoryScope("bogus"),
	}).Normalize()
	if err == nil || !strings.Contains(err.Error(), "memory_scope must be one of") {
		t.Fatalf("expected memory_scope validation error, got %v", err)
	}
}

func TestSessionsSpawnRequestNormalize_ParsesMemoryScope(t *testing.T) {
	req, err := (SessionsSpawnRequest{
		Message:     " run task ",
		AgentID:     " worker ",
		MemoryScope: state.AgentMemoryScope("local"),
		TimeoutMS:   500,
	}).Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.AgentID != "worker" || req.Message != "run task" {
		t.Fatalf("unexpected normalized request: %#v", req)
	}
	if req.MemoryScope != state.AgentMemoryScopeLocal {
		t.Fatalf("expected local memory scope, got %#v", req.MemoryScope)
	}
	if req.TimeoutMS != 500 {
		t.Fatalf("expected timeout to be preserved, got %d", req.TimeoutMS)
	}
}

func TestDecodeChatSendParams_RejectsNonStringPositional(t *testing.T) {
	_, err := DecodeChatSendParams(json.RawMessage(`[123,"hi"]`))
	if err == nil {
		t.Fatal("expected error for non-string positional to")
	}
}

func TestDecodeChatParams_OpenClawShapeCompatibility(t *testing.T) {
	sendReq, err := DecodeChatSendParams(json.RawMessage(`{"sessionKey":"npub1alice","message":"hello","idempotencyKey":"run-1","timeoutMs":1000}`))
	if err != nil {
		t.Fatalf("chat.send decode error: %v", err)
	}
	sendReq, err = sendReq.Normalize()
	if err != nil {
		t.Fatalf("chat.send normalize error: %v", err)
	}
	if sendReq.To != "npub1alice" || sendReq.Text != "hello" || sendReq.RunID != "run-1" {
		t.Fatalf("unexpected chat.send req: %#v", sendReq)
	}

	historyReq, err := DecodeChatHistoryParams(json.RawMessage(`{"sessionKey":"s1","limit":25}`))
	if err != nil {
		t.Fatalf("chat.history decode error: %v", err)
	}
	historyReq, err = historyReq.Normalize()
	if err != nil {
		t.Fatalf("chat.history normalize error: %v", err)
	}
	if historyReq.SessionID != "s1" || historyReq.Limit != 25 {
		t.Fatalf("unexpected chat.history req: %#v", historyReq)
	}

	abortReq, err := DecodeChatAbortParams(json.RawMessage(`{"sessionKey":"s1","runId":"run-1"}`))
	if err != nil {
		t.Fatalf("chat.abort decode error: %v", err)
	}
	abortReq, err = abortReq.Normalize()
	if err != nil {
		t.Fatalf("chat.abort normalize error: %v", err)
	}
	if abortReq.SessionID != "s1" || abortReq.RunID != "run-1" {
		t.Fatalf("unexpected chat.abort req: %#v", abortReq)
	}
}

func TestDecodeSessionsParams_OpenClawShapeCompatibility(t *testing.T) {
	sessionGetReq, err := DecodeSessionGetParams(json.RawMessage(`{"key":"s1","limit":10}`))
	if err != nil {
		t.Fatalf("session.get decode error: %v", err)
	}
	sessionGetReq, err = sessionGetReq.Normalize()
	if err != nil {
		t.Fatalf("session.get normalize error: %v", err)
	}
	if sessionGetReq.SessionID != "s1" {
		t.Fatalf("unexpected session.get request: %#v", sessionGetReq)
	}

	previewReq, err := DecodeSessionsPreviewParams(json.RawMessage(`{"keys":["s1","s2"],"limit":12,"maxChars":300}`))
	if err != nil {
		t.Fatalf("sessions.preview decode error: %v", err)
	}
	previewReq, err = previewReq.Normalize()
	if err != nil {
		t.Fatalf("sessions.preview normalize error: %v", err)
	}
	if previewReq.SessionID != "s1" || len(previewReq.Keys) != 2 {
		t.Fatalf("unexpected preview request: %#v", previewReq)
	}

	patchReq, err := DecodeSessionsPatchParams(json.RawMessage(`{"key":"s1","meta":{"k":"v"}}`))
	if err != nil {
		t.Fatalf("sessions.patch decode error: %v", err)
	}
	patchReq, err = patchReq.Normalize()
	if err != nil {
		t.Fatalf("sessions.patch normalize error: %v", err)
	}
	if patchReq.SessionID != "s1" {
		t.Fatalf("unexpected patch request: %#v", patchReq)
	}

	resetReq, err := DecodeSessionsResetParams(json.RawMessage(`{"sessionKey":"s1"}`))
	if err != nil {
		t.Fatalf("sessions.reset decode error: %v", err)
	}
	resetReq, err = resetReq.Normalize()
	if err != nil {
		t.Fatalf("sessions.reset normalize error: %v", err)
	}
	if resetReq.SessionID != "s1" {
		t.Fatalf("unexpected reset request: %#v", resetReq)
	}

	compactReq, err := DecodeSessionsCompactParams(json.RawMessage(`{"key":"s1","maxLines":15}`))
	if err != nil {
		t.Fatalf("sessions.compact decode error: %v", err)
	}
	compactReq, err = compactReq.Normalize()
	if err != nil {
		t.Fatalf("sessions.compact normalize error: %v", err)
	}
	if compactReq.SessionID != "s1" || compactReq.Keep != 15 {
		t.Fatalf("unexpected compact request: %#v", compactReq)
	}
}

func TestDecodeACPParams_CamelCaseCompatibility(t *testing.T) {
	dispatchReq, err := DecodeACPDispatchParams(json.RawMessage(`{"targetPubKey":"peer-1","instructions":"do it","contextMessages":[{"role":"user","content":"prior"}],"memoryScope":"project","toolProfile":"coding","enabledTools":["memory_search","memory_search"],"parentContext":{"sessionId":"sess-1","agentId":"worker"},"timeoutMs":1000}`))
	if err != nil {
		t.Fatalf("acp.dispatch decode error: %v", err)
	}
	dispatchReq, err = dispatchReq.Normalize()
	if err != nil {
		t.Fatalf("acp.dispatch normalize error: %v", err)
	}
	if dispatchReq.TargetPubKey != "peer-1" || dispatchReq.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("unexpected acp.dispatch request: %#v", dispatchReq)
	}
	if dispatchReq.ParentContext == nil || dispatchReq.ParentContext.SessionID != "sess-1" || dispatchReq.ParentContext.AgentID != "worker" {
		t.Fatalf("unexpected parent context: %#v", dispatchReq.ParentContext)
	}
	if len(dispatchReq.EnabledTools) != 1 || dispatchReq.EnabledTools[0] != "memory_search" {
		t.Fatalf("unexpected enabled tools: %#v", dispatchReq.EnabledTools)
	}

	pipelineReq, err := DecodeACPPipelineParams(json.RawMessage(`{"steps":[{"peerPubKey":"peer-1","instructions":"step","contextMessages":[{"role":"assistant","content":"ctx"}],"memoryScope":"local","toolProfile":"coding","enabledTools":["memory_store"],"parentContext":{"sessionId":"sess-2","agentId":"worker"},"timeoutMs":500}],"parallel":true}`))
	if err != nil {
		t.Fatalf("acp.pipeline decode error: %v", err)
	}
	pipelineReq, err = pipelineReq.Normalize()
	if err != nil {
		t.Fatalf("acp.pipeline normalize error: %v", err)
	}
	if len(pipelineReq.Steps) != 1 || pipelineReq.Steps[0].PeerPubKey != "peer-1" || pipelineReq.Steps[0].MemoryScope != state.AgentMemoryScopeLocal {
		t.Fatalf("unexpected acp.pipeline request: %#v", pipelineReq)
	}
}

func TestACPTaskSchemaNormalization(t *testing.T) {
	dispatchReq, err := DecodeACPDispatchParams(json.RawMessage(`{
		"target_pubkey":"peer-1",
		"task":{
			"goal_id":"goal-1",
			"title":"Implement envelope",
			"instructions":"Implement kind 38383 transport",
			"memory_scope":"project",
			"tool_profile":"coding",
			"enabled_tools":["read_file","apply_edits"],
			"expected_outputs":[{"name":"task-event","format":"json","required":true}],
			"acceptance_criteria":[{"description":"event schema round-trips","required":true}]
		}
	}`))
	if err != nil {
		t.Fatalf("DecodeACPDispatchParams: %v", err)
	}
	dispatchReq, err = dispatchReq.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if dispatchReq.Task == nil {
		t.Fatal("expected normalized task")
	}
	if dispatchReq.Instructions != "Implement kind 38383 transport" {
		t.Fatalf("expected instructions from task schema, got %q", dispatchReq.Instructions)
	}
	if dispatchReq.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("expected project memory scope, got %q", dispatchReq.MemoryScope)
	}
	if len(dispatchReq.EnabledTools) != 2 {
		t.Fatalf("unexpected enabled tools: %#v", dispatchReq.EnabledTools)
	}

	pipelineReq, err := DecodeACPPipelineParams(json.RawMessage(`{
		"steps":[{
			"peer_pubkey":"peer-2",
			"task":{
				"task_id":"task-2",
				"title":"Review result",
				"instructions":"Review the generated result",
				"memory_scope":"local",
				"tool_profile":"coding",
				"enabled_tools":["read_file"]
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("DecodeACPPipelineParams: %v", err)
	}
	pipelineReq, err = pipelineReq.Normalize()
	if err != nil {
		t.Fatalf("Normalize pipeline: %v", err)
	}
	if pipelineReq.Steps[0].Task == nil || pipelineReq.Steps[0].Task.TaskID != "task-2" {
		t.Fatalf("expected pipeline task schema, got %#v", pipelineReq.Steps[0].Task)
	}
	if pipelineReq.Steps[0].Instructions != "Review the generated result" {
		t.Fatalf("expected pipeline instructions from task, got %q", pipelineReq.Steps[0].Instructions)
	}
}

func TestACPTaskSchemaRejectsInvalidTaskFields(t *testing.T) {
	req := ACPDispatchRequest{
		TargetPubKey: "peer-1",
		Task: &state.TaskSpec{
			Title:        "Broken",
			Instructions: "",
			MemoryScope:  state.AgentMemoryScope("bogus"),
		},
	}
	_, err := req.Normalize()
	if err == nil {
		t.Fatal("expected normalize error for invalid task schema")
	}
}

func TestTasksCreateRequestNormalize_DerivesTaskFields(t *testing.T) {
	req, err := DecodeTasksCreateParams(json.RawMessage(`{
		"task":{
			"instructions":"  Review deployment output  ",
			"assigned_agent":" Worker ",
			"memory_scope":"project",
			"enabled_tools":["read_file","read_file"," "]
		}
	}`))
	if err != nil {
		t.Fatalf("DecodeTasksCreateParams: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if req.Task.Title != "Review deployment output" {
		t.Fatalf("expected derived title, got %q", req.Task.Title)
	}
	if req.Task.AssignedAgent != "worker" {
		t.Fatalf("expected normalized assigned agent, got %q", req.Task.AssignedAgent)
	}
	if req.Task.MemoryScope != state.AgentMemoryScopeProject {
		t.Fatalf("expected project memory scope, got %q", req.Task.MemoryScope)
	}
	if len(req.Task.EnabledTools) != 1 || req.Task.EnabledTools[0] != "read_file" {
		t.Fatalf("unexpected enabled tools: %#v", req.Task.EnabledTools)
	}
}

func TestDecodeTasksGetParams_CamelCaseCompatibility(t *testing.T) {
	req, err := DecodeTasksGetParams(json.RawMessage(`{"taskId":"task-1","runsLimit":7}`))
	if err != nil {
		t.Fatalf("DecodeTasksGetParams: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if req.TaskID != "task-1" || req.RunsLimit != 7 {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestDecodeConfigPutParams_ArrayMode(t *testing.T) {
	raw := json.RawMessage(`[{"dm":{"policy":"open"}}]`)
	req, err := DecodeConfigPutParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if req.Config.DM.Policy != "open" {
		t.Fatalf("unexpected policy: %q", req.Config.DM.Policy)
	}
}

func TestDecodeConfigPutParams_ArrayModeWithPrecondition(t *testing.T) {
	raw := json.RawMessage(`[{"dm":{"policy":"open"}},{"expected_version":2,"expected_event":"abc"}]`)
	req, err := DecodeConfigPutParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !req.ExpectedVersionSet || req.ExpectedVersion != 2 || req.ExpectedEvent != "abc" {
		t.Fatalf("unexpected precondition: %+v", req)
	}
}

func TestDecodeConfigPutParams_ExpectedVersionZeroIsExplicit(t *testing.T) {
	req, err := DecodeConfigPutParams(json.RawMessage(`{"config":{"dm":{"policy":"open"}},"expected_version":0}`))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !req.ExpectedVersionSet || req.ExpectedVersion != 0 {
		t.Fatalf("expected explicit expected_version=0, got: %+v", req)
	}
}

func TestDecodeMCPListParams_EmptyAllowed(t *testing.T) {
	req, err := DecodeMCPListParams(nil)
	if err != nil {
		t.Fatalf("DecodeMCPListParams error: %v", err)
	}
	if _, err := req.Normalize(); err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
}

func TestMCPPutRequestNormalize_RequiresServerAndConfig(t *testing.T) {
	if _, err := (MCPPutRequest{Server: " ", Config: map[string]any{"type": "stdio"}}).Normalize(); err == nil {
		t.Fatal("expected missing server error")
	}
	if _, err := (MCPPutRequest{Server: "demo"}).Normalize(); err == nil || !strings.Contains(err.Error(), "config is required") {
		t.Fatalf("expected missing config error, got %v", err)
	}
	req, err := (MCPPutRequest{Server: " demo ", Config: map[string]any{"type": "stdio"}}).Normalize()
	if err != nil {
		t.Fatalf("Normalize error: %v", err)
	}
	if req.Server != "demo" {
		t.Fatalf("expected trimmed server, got %#v", req)
	}
}

func TestMCPTestRequestNormalize_RejectsNegativeTimeout(t *testing.T) {
	_, err := (MCPTestRequest{Server: "demo", TimeoutMS: -1}).Normalize()
	if err == nil || !strings.Contains(err.Error(), "timeout_ms") {
		t.Fatalf("expected timeout validation error, got %v", err)
	}
}

func TestDecodeListPutParams_ArrayMode(t *testing.T) {
	raw := json.RawMessage(`["allowlist",["npub1","npub2","npub1"," "]]`)
	req, err := DecodeListPutParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.Name != "allowlist" {
		t.Fatalf("unexpected name: %q", req.Name)
	}
	if len(req.Items) != 2 {
		t.Fatalf("unexpected item count: %d", len(req.Items))
	}
}

func TestDecodeListPutParams_ArrayModeWithPrecondition(t *testing.T) {
	raw := json.RawMessage(`["allowlist",["npub1"],{"expected_version":3,"expected_event":"evt-1"}]`)
	req, err := DecodeListPutParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if !req.ExpectedVersionSet || req.ExpectedVersion != 3 || req.ExpectedEvent != "evt-1" {
		t.Fatalf("unexpected precondition: %+v", req)
	}
}

func TestDecodeListPutParams_ExpectedVersionZeroIsExplicit(t *testing.T) {
	req, err := DecodeListPutParams(json.RawMessage(`{"name":"allowlist","items":["npub1"],"expected_version":0}`))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if !req.ExpectedVersionSet || req.ExpectedVersion != 0 {
		t.Fatalf("expected explicit expected_version=0, got: %+v", req)
	}
}

func TestDecodeListGetParams_RejectsNonStringPositional(t *testing.T) {
	_, err := DecodeListGetParams(json.RawMessage(`[123]`))
	if err == nil {
		t.Fatal("expected error for non-string positional list name")
	}
}

func TestMemorySearchNormalize_TruncatesByRunes(t *testing.T) {
	req := MemorySearchRequest{Query: strings.Repeat("🙂", 300), Limit: 1}
	normalized, err := req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if utf8.RuneCountInString(normalized.Query) != 256 {
		t.Fatalf("query rune count = %d, want 256", utf8.RuneCountInString(normalized.Query))
	}
	if !utf8.ValidString(normalized.Query) {
		t.Fatal("normalized query is not valid UTF-8")
	}
}

func TestSupportedMethodsIncludesRelayPolicyGet(t *testing.T) {
	found := false
	for _, method := range SupportedMethods() {
		if method == MethodRelayPolicyGet {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("%s not found in supported methods", MethodRelayPolicyGet)
	}
}

func TestSupportedMethodsIncludesTaskMethods(t *testing.T) {
	required := []string{MethodTasksCreate, MethodTasksGet, MethodTasksList, MethodTasksCancel, MethodTasksResume}
	for _, want := range required {
		found := false
		for _, method := range SupportedMethods() {
			if method == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not found in supported methods", want)
		}
	}
}

func TestSupportedMethodsIncludesPhaseAMethods(t *testing.T) {
	required := []string{MethodHealth, MethodAgent, MethodAgentWait, MethodAgentIdentityGet, MethodConfigSet, MethodConfigPatch, MethodChatHistory, MethodSessionsList}
	for _, want := range required {
		found := false
		for _, method := range SupportedMethods() {
			if method == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not found in supported methods", want)
		}
	}
}

func TestDecodeAgentParamsAndWait_ArrayMode(t *testing.T) {
	agentReq, err := DecodeAgentParams(json.RawMessage(`["hello","s1","ctx",1500]`))
	if err != nil {
		t.Fatalf("agent decode error: %v", err)
	}
	agentReq, err = agentReq.Normalize()
	if err != nil {
		t.Fatalf("agent normalize error: %v", err)
	}
	if agentReq.Message != "hello" || agentReq.SessionID != "s1" || agentReq.TimeoutMS != 1500 {
		t.Fatalf("unexpected agent req: %#v", agentReq)
	}

	waitReq, err := DecodeAgentWaitParams(json.RawMessage(`["run-1",1000]`))
	if err != nil {
		t.Fatalf("agent.wait decode error: %v", err)
	}
	waitReq, err = waitReq.Normalize()
	if err != nil {
		t.Fatalf("agent.wait normalize error: %v", err)
	}
	if waitReq.RunID != "run-1" || waitReq.TimeoutMS != 1000 {
		t.Fatalf("unexpected agent.wait req: %#v", waitReq)
	}

	identityReq, err := DecodeAgentIdentityParams(json.RawMessage(`["s1","main"]`))
	if err != nil {
		t.Fatalf("agent.identity decode error: %v", err)
	}
	identityReq, err = identityReq.Normalize()
	if err != nil {
		t.Fatalf("agent.identity normalize error: %v", err)
	}
	if identityReq.SessionID != "s1" || identityReq.AgentID != "main" {
		t.Fatalf("unexpected agent.identity req: %#v", identityReq)
	}
}

func TestDecodeConfigSetParams_ArrayMode(t *testing.T) {
	req, err := DecodeConfigSetParams(json.RawMessage(`["dm.policy","open"]`))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.Key != "dm.policy" {
		t.Fatalf("unexpected key: %q", req.Key)
	}
}

func TestDecodeConfigParams_RawCompatibility(t *testing.T) {
	putReq, err := DecodeConfigPutParams(json.RawMessage(`{"config":{"dm":{"policy":"open"}},"base_hash":"abc"}`))
	if err != nil {
		t.Fatalf("config.put decode error: %v", err)
	}
	putReq, err = putReq.Normalize()
	if err != nil {
		t.Fatalf("config.put normalize error: %v", err)
	}
	if putReq.BaseHash != "abc" {
		t.Fatalf("unexpected config.put base hash: %#v", putReq)
	}

	putReq, err = DecodeConfigPutParams(json.RawMessage(`[{"dm":{"policy":"open"}},{"base_hash":"def"}]`))
	if err != nil {
		t.Fatalf("config.put array decode error: %v", err)
	}
	putReq, err = putReq.Normalize()
	if err != nil {
		t.Fatalf("config.put array normalize error: %v", err)
	}
	if putReq.BaseHash != "def" {
		t.Fatalf("unexpected config.put array base hash: %#v", putReq)
	}

	setReq, err := DecodeConfigSetParams(json.RawMessage(`{"raw":"{\"version\":1,\"dm\":{\"policy\":\"open\"}}","base_hash":"abc"}`))
	if err != nil {
		t.Fatalf("config.set decode error: %v", err)
	}
	setReq, err = setReq.Normalize()
	if err != nil {
		t.Fatalf("config.set normalize error: %v", err)
	}
	if setReq.Raw == "" || setReq.BaseHash != "abc" {
		t.Fatalf("unexpected config.set request: %#v", setReq)
	}

	applyReq, err := DecodeConfigApplyParams(json.RawMessage(`["{\"version\":2,\"dm\":{\"policy\":\"pairing\"}}"]`))
	if err != nil {
		t.Fatalf("config.apply decode error: %v", err)
	}
	applyReq, err = applyReq.Normalize()
	if err != nil {
		t.Fatalf("config.apply normalize error: %v", err)
	}
	if applyReq.Raw == "" {
		t.Fatalf("expected raw config apply request: %#v", applyReq)
	}

	patchReq, err := DecodeConfigPatchParams(json.RawMessage(`{"raw":"{\"dm\":{\"policy\":\"open\"}}","session_key":"s1"}`))
	if err != nil {
		t.Fatalf("config.patch decode error: %v", err)
	}
	patchReq, err = patchReq.Normalize()
	if err != nil {
		t.Fatalf("config.patch normalize error: %v", err)
	}
	if patchReq.Raw == "" || patchReq.SessionKey != "s1" {
		t.Fatalf("unexpected config.patch request: %#v", patchReq)
	}
}

func TestDecodeConfigRawHelpers(t *testing.T) {
	cfg, err := DecodeConfigDocFromRaw(`{"version":3,"dm":{"policy":"open"}}`)
	if err != nil {
		t.Fatalf("DecodeConfigDocFromRaw error: %v", err)
	}
	if cfg.DM.Policy != "open" || cfg.Version != 3 {
		t.Fatalf("unexpected config from raw: %#v", cfg)
	}
	patch, err := DecodeConfigPatchFromRaw(`{"dm":{"policy":"pairing"}}`)
	if err != nil {
		t.Fatalf("DecodeConfigPatchFromRaw error: %v", err)
	}
	dm, _ := patch["dm"].(map[string]any)
	if dm["policy"] != "pairing" {
		t.Fatalf("unexpected patch from raw: %#v", patch)
	}
}

func TestDecodeSessionsListParams_ArrayMode(t *testing.T) {
	req, err := DecodeSessionsListParams(json.RawMessage(`[25]`))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.Limit != 25 {
		t.Fatalf("unexpected limit: %d", req.Limit)
	}
}

func TestDecodeSessionsListParams_OpenClawExtendedFields(t *testing.T) {
	req, err := DecodeSessionsListParams(json.RawMessage(`{"limit":20,"includeGlobal":true,"includeUnknown":false,"activeMinutes":30,"search":"alice","agentId":"main"}`))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.Limit != 20 || !req.IncludeGlobal || req.ActiveMinutes != 30 || req.Search != "alice" || req.AgentID != "main" {
		t.Fatalf("unexpected sessions.list request: %#v", req)
	}
}

func TestDecodeSessionsPatchParams_ArrayMode(t *testing.T) {
	req, err := DecodeSessionsPatchParams(json.RawMessage(`["s1",{"k":"v"}]`))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.SessionID != "s1" || req.Meta["k"] != "v" {
		t.Fatalf("unexpected patch request: %#v", req)
	}
}

func TestDecodeSessionsCompactParams_ArrayMode(t *testing.T) {
	req, err := DecodeSessionsCompactParams(json.RawMessage(`["s1",10]`))
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.SessionID != "s1" || req.Keep != 10 {
		t.Fatalf("unexpected compact request: %#v", req)
	}
}

func TestDecodeAgentsFilesParams_ArrayMode(t *testing.T) {
	listReq, err := DecodeAgentsFilesListParams(json.RawMessage(`["main",20]`))
	if err != nil {
		t.Fatalf("files.list decode error: %v", err)
	}
	listReq, err = listReq.Normalize()
	if err != nil {
		t.Fatalf("files.list normalize error: %v", err)
	}
	if listReq.AgentID != "main" || listReq.Limit != 20 {
		t.Fatalf("unexpected files.list request: %#v", listReq)
	}

	getReq, err := DecodeAgentsFilesGetParams(json.RawMessage(`["main","instructions.md"]`))
	if err != nil {
		t.Fatalf("files.get decode error: %v", err)
	}
	getReq, err = getReq.Normalize()
	if err != nil {
		t.Fatalf("files.get normalize error: %v", err)
	}
	if getReq.AgentID != "main" || getReq.Name != "instructions.md" {
		t.Fatalf("unexpected files.get request: %#v", getReq)
	}

	setReq, err := DecodeAgentsFilesSetParams(json.RawMessage(`["main","instructions.md","hello"]`))
	if err != nil {
		t.Fatalf("files.set decode error: %v", err)
	}
	setReq, err = setReq.Normalize()
	if err != nil {
		t.Fatalf("files.set normalize error: %v", err)
	}
	if setReq.Content != "hello" {
		t.Fatalf("unexpected files.set request: %#v", setReq)
	}
}

func TestDecodeAgentsRoutingParams(t *testing.T) {
	assignReq, err := DecodeAgentsAssignParams(json.RawMessage(`{"agent_id":"Main","session_id":" s-1 "}`))
	if err != nil {
		t.Fatalf("agents.assign decode error: %v", err)
	}
	assignReq, err = assignReq.Normalize()
	if err != nil {
		t.Fatalf("agents.assign normalize error: %v", err)
	}
	if assignReq.AgentID != "main" || assignReq.SessionID != "s-1" {
		t.Fatalf("unexpected agents.assign request: %#v", assignReq)
	}

	unassignReq, err := DecodeAgentsUnassignParams(json.RawMessage(`{"session_id":" s-1 "}`))
	if err != nil {
		t.Fatalf("agents.unassign decode error: %v", err)
	}
	unassignReq, err = unassignReq.Normalize()
	if err != nil {
		t.Fatalf("agents.unassign normalize error: %v", err)
	}
	if unassignReq.SessionID != "s-1" {
		t.Fatalf("unexpected agents.unassign request: %#v", unassignReq)
	}

	activeReq, err := DecodeAgentsActiveParams(json.RawMessage(`{"limit":999}`))
	if err != nil {
		t.Fatalf("agents.active decode error: %v", err)
	}
	activeReq, err = activeReq.Normalize()
	if err != nil {
		t.Fatalf("agents.active normalize error: %v", err)
	}
	if activeReq.Limit != 500 {
		t.Fatalf("unexpected agents.active normalized limit: %d", activeReq.Limit)
	}

	activeDefaultReq, err := DecodeAgentsActiveParams(json.RawMessage(``))
	if err != nil {
		t.Fatalf("agents.active empty decode error: %v", err)
	}
	activeDefaultReq, err = activeDefaultReq.Normalize()
	if err != nil {
		t.Fatalf("agents.active default normalize error: %v", err)
	}
	if activeDefaultReq.Limit != 100 {
		t.Fatalf("unexpected agents.active default limit: %d", activeDefaultReq.Limit)
	}
}

func TestDecodeChannelsJoinParams_TypeValidation(t *testing.T) {
	joinReq, err := DecodeChannelsJoinParams(json.RawMessage(`{"group_address":"relay.example.com'group-1"}`))
	if err != nil {
		t.Fatalf("channels.join decode error: %v", err)
	}
	joinReq, err = joinReq.Normalize()
	if err != nil {
		t.Fatalf("channels.join normalize error: %v", err)
	}
	if joinReq.Type != "nip29-group" {
		t.Fatalf("expected default type nip29-group, got: %#v", joinReq)
	}

	badReq, err := DecodeChannelsJoinParams(json.RawMessage(`{"type":"slack","group_address":"relay.example.com'group-1"}`))
	if err != nil {
		t.Fatalf("channels.join bad type decode error: %v", err)
	}
	if _, err := badReq.Normalize(); err == nil {
		t.Fatalf("expected unsupported channel type error")
	}
}

func TestSupportedMethodsIncludesAgentsMethods(t *testing.T) {
	required := []string{
		MethodAgentsList,
		MethodAgentsCreate,
		MethodAgentsUpdate,
		MethodAgentsDelete,
		MethodAgentsAssign,
		MethodAgentsUnassign,
		MethodAgentsActive,
		MethodAgentsFilesList,
		MethodAgentsFilesGet,
		MethodAgentsFilesSet,
	}
	for _, want := range required {
		found := false
		for _, method := range SupportedMethods() {
			if method == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not found in supported methods", want)
		}
	}
}

func TestDecodeModelsToolsSkillsParams(t *testing.T) {
	modelsReq, err := DecodeModelsListParams(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("models.list decode error: %v", err)
	}
	if _, err := modelsReq.Normalize(); err != nil {
		t.Fatalf("models.list normalize error: %v", err)
	}

	toolsReq, err := DecodeToolsCatalogParams(json.RawMessage(`{"agent_id":"Main"}`))
	if err != nil {
		t.Fatalf("tools.catalog decode error: %v", err)
	}
	toolsReq, err = toolsReq.Normalize()
	if err != nil {
		t.Fatalf("tools.catalog normalize error: %v", err)
	}
	if toolsReq.AgentID != "main" {
		t.Fatalf("unexpected tools catalog agent id: %#v", toolsReq)
	}

	installReq, err := DecodeSkillsInstallParams(json.RawMessage(`{"agent_id":"Main","name":"nostr-core","install_id":"builtin"}`))
	if err != nil {
		t.Fatalf("skills.install decode error: %v", err)
	}
	installReq, err = installReq.Normalize()
	if err != nil {
		t.Fatalf("skills.install normalize error: %v", err)
	}
	if installReq.AgentID != "main" || installReq.TimeoutMS <= 0 {
		t.Fatalf("expected normalized install request, got: %#v", installReq)
	}

	updateReq, err := DecodeSkillsUpdateParams(json.RawMessage(`{"agent_id":"Main","skill_key":"Nostr-Core","api_key":"  abc  ","env":{" K ":" V "}}`))
	if err != nil {
		t.Fatalf("skills.update decode error: %v", err)
	}
	updateReq, err = updateReq.Normalize()
	if err != nil {
		t.Fatalf("skills.update normalize error: %v", err)
	}
	if updateReq.SkillKey != "nostr-core" {
		t.Fatalf("unexpected skill key normalization: %#v", updateReq)
	}
	if updateReq.APIKey == nil || *updateReq.APIKey != "abc" {
		t.Fatalf("unexpected api key normalization: %#v", updateReq)
	}
	if updateReq.AgentID != "main" {
		t.Fatalf("unexpected agent id normalization: %#v", updateReq)
	}
	if updateReq.Env["K"] != "V" {
		t.Fatalf("unexpected env normalization: %#v", updateReq.Env)
	}

	pluginInstallReq, err := DecodePluginsInstallParams(json.RawMessage(`{"plugin_id":"codegen","install":{"source":"path","sourcePath":"./ext/codegen"}}`))
	if err != nil {
		t.Fatalf("plugins.install decode error: %v", err)
	}
	pluginInstallReq, err = pluginInstallReq.Normalize()
	if err != nil {
		t.Fatalf("plugins.install normalize error: %v", err)
	}
	if pluginInstallReq.PluginID != "codegen" || pluginInstallReq.EnableEntry == nil || !*pluginInstallReq.EnableEntry {
		t.Fatalf("unexpected plugins.install normalization: %#v", pluginInstallReq)
	}

	pluginUninstallReq, err := DecodePluginsUninstallParams(json.RawMessage(`{"plugin_id":"codegen"}`))
	if err != nil {
		t.Fatalf("plugins.uninstall decode error: %v", err)
	}
	pluginUninstallReq, err = pluginUninstallReq.Normalize()
	if err != nil {
		t.Fatalf("plugins.uninstall normalize error: %v", err)
	}
	if pluginUninstallReq.PluginID != "codegen" {
		t.Fatalf("unexpected plugins.uninstall normalization: %#v", pluginUninstallReq)
	}

	invalidInstallReq, err := DecodePluginsInstallParams(json.RawMessage(`{"plugin_id":"../evil","install":{"source":"path","sourcePath":"./x"}}`))
	if err != nil {
		t.Fatalf("plugins.install invalid-id decode error: %v", err)
	}
	if _, err := invalidInstallReq.Normalize(); err == nil {
		t.Fatalf("expected invalid plugin_id normalization error")
	}

	pluginUpdateReq, err := DecodePluginsUpdateParams(json.RawMessage(`{"plugin_ids":[" codegen ","", "other"],"dry_run":true}`))
	if err != nil {
		t.Fatalf("plugins.update decode error: %v", err)
	}
	pluginUpdateReq, err = pluginUpdateReq.Normalize()
	if err != nil {
		t.Fatalf("plugins.update normalize error: %v", err)
	}
	if len(pluginUpdateReq.PluginIDs) != 2 || pluginUpdateReq.PluginIDs[0] != "codegen" || pluginUpdateReq.PluginIDs[1] != "other" || !pluginUpdateReq.DryRun {
		t.Fatalf("unexpected plugins.update normalization: %#v", pluginUpdateReq)
	}
}

func TestSupportedMethodsIncludesModelsToolsSkillsMethods(t *testing.T) {
	required := []string{MethodModelsList, MethodToolsCatalog, MethodSkillsStatus, MethodSkillsBins, MethodSkillsInstall, MethodSkillsUpdate, MethodPluginsInstall, MethodPluginsUninstall, MethodPluginsUpdate}
	for _, want := range required {
		found := false
		for _, method := range SupportedMethods() {
			if method == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not found in supported methods", want)
		}
	}
}

func TestDecodeNodeDevicePairingParams(t *testing.T) {
	nodeReq, err := DecodeNodePairRequestParams(json.RawMessage(`{"node_id":"n1"}`))
	if err != nil {
		t.Fatalf("node.pair.request decode error: %v", err)
	}
	if _, err := nodeReq.Normalize(); err != nil {
		t.Fatalf("node.pair.request normalize error: %v", err)
	}

	rotateReq, err := DecodeDeviceTokenRotateParams(json.RawMessage(`{"device_id":"d1","role":"node"}`))
	if err != nil {
		t.Fatalf("device.token.rotate decode error: %v", err)
	}
	if _, err := rotateReq.Normalize(); err != nil {
		t.Fatalf("device.token.rotate normalize error: %v", err)
	}
}

func TestSupportedMethodsIncludesPairingMethods(t *testing.T) {
	required := []string{MethodNodePairRequest, MethodNodePairList, MethodNodePairApprove, MethodNodePairReject, MethodNodePairVerify, MethodDevicePairList, MethodDevicePairApprove, MethodDevicePairReject, MethodDevicePairRemove, MethodDeviceTokenRotate, MethodDeviceTokenRevoke}
	for _, want := range required {
		found := false
		for _, method := range SupportedMethods() {
			if method == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not found in supported methods", want)
		}
	}
}

func TestSupportedMethodsIncludesStatusAlias(t *testing.T) {
	required := []string{MethodStatus, MethodStatusAlias}
	for _, want := range required {
		found := false
		for _, method := range SupportedMethods() {
			if method == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not found in supported methods", want)
		}
	}
}

func TestDecodeMethodParamsSupportsCamelCaseAliases(t *testing.T) {
	patchReq, err := DecodeSessionsPatchParams(json.RawMessage(`{"sessionId":"s1","meta":{"k":"v"}}`))
	if err != nil {
		t.Fatalf("sessions.patch decode error: %v", err)
	}
	patchReq, err = patchReq.Normalize()
	if err != nil {
		t.Fatalf("sessions.patch normalize error: %v", err)
	}
	if patchReq.SessionID != "s1" {
		t.Fatalf("unexpected session id: %#v", patchReq)
	}

	nodeReq, err := DecodeNodePairRequestParams(json.RawMessage(`{"nodeId":"n1","displayName":"Node 1","coreVersion":"1.0","remoteIp":"10.0.0.2"}`))
	if err != nil {
		t.Fatalf("node.pair.request decode error: %v", err)
	}
	nodeReq, err = nodeReq.Normalize()
	if err != nil {
		t.Fatalf("node.pair.request normalize error: %v", err)
	}
	if nodeReq.NodeID != "n1" || nodeReq.DisplayName != "Node 1" || nodeReq.CoreVersion != "1.0" || nodeReq.RemoteIP != "10.0.0.2" {
		t.Fatalf("unexpected node request: %#v", nodeReq)
	}

	cfgReq, err := DecodeConfigPutParams(json.RawMessage(`{"config":{"version":1,"dm":{"policy":"open"},"relays":{"read":["wss://r"],"write":["wss://r"]}},"expectedVersion":2,"expectedEvent":"evt-1"}`))
	if err != nil {
		t.Fatalf("config.put decode error: %v", err)
	}
	cfgReq, err = cfgReq.Normalize()
	if err != nil {
		t.Fatalf("config.put normalize error: %v", err)
	}
	if !cfgReq.ExpectedVersionSet || cfgReq.ExpectedVersion != 2 || cfgReq.ExpectedEvent != "evt-1" {
		t.Fatalf("unexpected config preconditions: %#v", cfgReq)
	}
}

func TestDecodeNodeInvokeAndCronParams(t *testing.T) {
	invokeReq, err := DecodeNodeInvokeParams(json.RawMessage(`{"nodeId":"n1","command":"ping","args":{"k":"v"},"timeoutMs":1234}`))
	if err != nil {
		t.Fatalf("node.invoke decode error: %v", err)
	}
	invokeReq, err = invokeReq.Normalize()
	if err != nil {
		t.Fatalf("node.invoke normalize error: %v", err)
	}
	if invokeReq.NodeID != "n1" || invokeReq.Command != "ping" || invokeReq.TimeoutMS != 1234 {
		t.Fatalf("unexpected node.invoke request: %#v", invokeReq)
	}

	eventReq, err := DecodeNodeEventParams(json.RawMessage(`{"runId":"r1","type":"progress","status":"running"}`))
	if err != nil {
		t.Fatalf("node.event decode error: %v", err)
	}
	eventReq, err = eventReq.Normalize()
	if err != nil {
		t.Fatalf("node.event normalize error: %v", err)
	}
	if eventReq.RunID != "r1" || eventReq.Type != "progress" {
		t.Fatalf("unexpected node.event request: %#v", eventReq)
	}

	cronReq, err := DecodeCronAddParams(json.RawMessage(`{"schedule":"* * * * *","method":"status.get","enabled":true}`))
	if err != nil {
		t.Fatalf("cron.add decode error: %v", err)
	}
	cronReq, err = cronReq.Normalize()
	if err != nil {
		t.Fatalf("cron.add normalize error: %v", err)
	}
	if cronReq.Method != "status.get" || cronReq.Schedule == "" {
		t.Fatalf("unexpected cron.add request: %#v", cronReq)
	}

	runsReq, err := DecodeCronRunsParams(json.RawMessage(`["job-1",25]`))
	if err != nil {
		t.Fatalf("cron.runs decode error: %v", err)
	}
	runsReq, err = runsReq.Normalize()
	if err != nil {
		t.Fatalf("cron.runs normalize error: %v", err)
	}
	if runsReq.ID != "job-1" || runsReq.Limit != 25 {
		t.Fatalf("unexpected cron.runs request: %#v", runsReq)
	}
}

func TestDecodeExecSecretsWizardTalkVoicewakeAndTTSParams(t *testing.T) {
	execReq, err := DecodeExecApprovalRequestParams(json.RawMessage(`{"nodeId":"n1","command":"ls","timeoutMs":5000}`))
	if err != nil {
		t.Fatalf("exec.approval.request decode error: %v", err)
	}
	execReq, err = execReq.Normalize()
	if err != nil {
		t.Fatalf("exec.approval.request normalize error: %v", err)
	}
	if execReq.NodeID != "n1" || execReq.Command != "ls" || execReq.TimeoutMS != 5000 {
		t.Fatalf("unexpected exec approval request: %#v", execReq)
	}

	waitReq, err := DecodeExecApprovalWaitDecisionParams(json.RawMessage(`{"id":"approval-1","timeoutMs":1000}`))
	if err != nil {
		t.Fatalf("exec.approval.waitDecision decode error: %v", err)
	}
	waitReq, err = waitReq.Normalize()
	if err != nil {
		t.Fatalf("exec.approval.waitDecision normalize error: %v", err)
	}
	if waitReq.ID != "approval-1" || waitReq.TimeoutMS != 1000 {
		t.Fatalf("unexpected exec approval wait request: %#v", waitReq)
	}

	secretsReq, err := DecodeSecretsResolveParams(json.RawMessage(`{"commandName":"memory status","targetIds":["talk.apiKey"]}`))
	if err != nil {
		t.Fatalf("secrets.resolve decode error: %v", err)
	}
	secretsReq, err = secretsReq.Normalize()
	if err != nil {
		t.Fatalf("secrets.resolve normalize error: %v", err)
	}
	if secretsReq.CommandName != "memory status" || len(secretsReq.TargetIDs) != 1 {
		t.Fatalf("unexpected secrets.resolve request: %#v", secretsReq)
	}

	wizardReq, err := DecodeWizardStartParams(json.RawMessage(`{"mode":"remote"}`))
	if err != nil {
		t.Fatalf("wizard.start decode error: %v", err)
	}
	wizardReq, err = wizardReq.Normalize()
	if err != nil {
		t.Fatalf("wizard.start normalize error: %v", err)
	}
	if wizardReq.Mode != "remote" {
		t.Fatalf("unexpected wizard.start request: %#v", wizardReq)
	}

	updateReq, err := DecodeUpdateRunParams(json.RawMessage(`{"force":true}`))
	if err != nil {
		t.Fatalf("update.run decode error: %v", err)
	}
	if _, err := updateReq.Normalize(); err != nil {
		t.Fatalf("update.run normalize error: %v", err)
	}

	hbReq, err := DecodeSetHeartbeatsParams(json.RawMessage(`{"enabled":true,"interval_ms":30000}`))
	if err != nil {
		t.Fatalf("set-heartbeats decode error: %v", err)
	}
	hbReq, err = hbReq.Normalize()
	if err != nil {
		t.Fatalf("set-heartbeats normalize error: %v", err)
	}
	if hbReq.IntervalMS != 30000 {
		t.Fatalf("unexpected set-heartbeats request: %#v", hbReq)
	}
	missingEnabledReq, err := DecodeSetHeartbeatsParams(json.RawMessage(`{"interval_ms":30000}`))
	if err != nil {
		t.Fatalf("set-heartbeats decode missing-enabled error: %v", err)
	}
	if _, err := missingEnabledReq.Normalize(); err == nil {
		t.Fatalf("expected set-heartbeats normalize to require enabled")
	}
	wakeReq, err := DecodeWakeParams(json.RawMessage(`{"agent_id":" main ","source":"manual","mode":"now","text":"wake now"}`))
	if err != nil {
		t.Fatalf("wake decode error: %v", err)
	}
	wakeReq, err = wakeReq.Normalize()
	if err != nil {
		t.Fatalf("wake normalize error: %v", err)
	}
	if wakeReq.AgentID != "main" || wakeReq.Mode != "now" || wakeReq.Text != "wake now" {
		t.Fatalf("unexpected wake request: %#v", wakeReq)
	}
	if _, err := (WakeRequest{Source: "manual", Mode: "typo", Text: "wake now"}).Normalize(); err == nil {
		t.Fatalf("expected wake normalize to reject invalid mode")
	}
	if _, err := (WakeRequest{Source: "manual", Mode: "now"}).Normalize(); err == nil {
		t.Fatalf("expected wake normalize to require text")
	}

	systemEventReq, err := DecodeSystemEventParams(json.RawMessage(`{"text":"Node: up","deviceId":"mac-1","roles":["control"]}`))
	if err != nil {
		t.Fatalf("system-event decode error: %v", err)
	}
	systemEventReq, err = systemEventReq.Normalize()
	if err != nil {
		t.Fatalf("system-event normalize error: %v", err)
	}
	if systemEventReq.Text != "Node: up" || systemEventReq.DeviceID != "mac-1" || len(systemEventReq.Roles) != 1 {
		t.Fatalf("unexpected system-event request: %#v", systemEventReq)
	}

	sendReq, err := DecodeSendParams(json.RawMessage(`{"to":"0000000000000000000000000000000000000000000000000000000000000001","message":"hello","idempotencyKey":"idem-1"}`))
	if err != nil {
		t.Fatalf("send decode error: %v", err)
	}
	sendReq, err = sendReq.Normalize()
	if err != nil {
		t.Fatalf("send normalize error: %v", err)
	}
	if sendReq.To != "0000000000000000000000000000000000000000000000000000000000000001" || sendReq.Message != "hello" || sendReq.IdempotencyKey != "idem-1" {
		t.Fatalf("unexpected send request: %#v", sendReq)
	}

	invalidSendReq, err := DecodeSendParams(json.RawMessage(`{"to":"invalid","message":"hello"}`))
	if err != nil {
		t.Fatalf("send decode error: %v", err)
	}
	_, err = invalidSendReq.Normalize()
	if err == nil {
		t.Fatalf("expected send normalize to fail with invalid npub")
	}

	browserReq, err := DecodeBrowserRequestParams(json.RawMessage(`{"method":"get","path":"/status"}`))
	if err != nil {
		t.Fatalf("browser.request decode error: %v", err)
	}
	browserReq, err = browserReq.Normalize()
	if err != nil {
		t.Fatalf("browser.request normalize error: %v", err)
	}
	if browserReq.Method != "GET" || browserReq.Path != "/status" {
		t.Fatalf("unexpected browser.request request: %#v", browserReq)
	}

	voicewakeReq, err := DecodeVoicewakeSetParams(json.RawMessage(`{"triggers":[" openclaw ","metiq"]}`))
	if err != nil {
		t.Fatalf("voicewake.set decode error: %v", err)
	}
	voicewakeReq, err = voicewakeReq.Normalize()
	if err != nil {
		t.Fatalf("voicewake.set normalize error: %v", err)
	}
	if len(voicewakeReq.Triggers) != 2 || voicewakeReq.Triggers[0] != "openclaw" {
		t.Fatalf("unexpected voicewake.set request: %#v", voicewakeReq)
	}

	ttsSetReq, err := DecodeTTSSetProviderParams(json.RawMessage(`["openai"]`))
	if err != nil {
		t.Fatalf("tts.setProvider decode error: %v", err)
	}
	ttsSetReq, err = ttsSetReq.Normalize()
	if err != nil {
		t.Fatalf("tts.setProvider normalize error: %v", err)
	}
	if ttsSetReq.Provider != "openai" {
		t.Fatalf("unexpected tts.setProvider request: %#v", ttsSetReq)
	}

	skillsBinsReq, err := DecodeSkillsBinsParams(json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("skills.bins decode error: %v", err)
	}
	skillsBinsReq, err = skillsBinsReq.Normalize()
	if err != nil {
		t.Fatalf("skills.bins normalize error: %v", err)
	}
	if skillsBinsReq != (SkillsBinsRequest{}) {
		t.Fatalf("unexpected skills.bins request: %#v", skillsBinsReq)
	}

	ttsReq, err := DecodeTTSConvertParams(json.RawMessage(`{"text":"hello","provider":"openai"}`))
	if err != nil {
		t.Fatalf("tts.convert decode error: %v", err)
	}
	ttsReq, err = ttsReq.Normalize()
	if err != nil {
		t.Fatalf("tts.convert normalize error: %v", err)
	}
	if ttsReq.Text != "hello" || ttsReq.Provider != "openai" {
		t.Fatalf("unexpected tts.convert request: %#v", ttsReq)
	}
}

func TestSupportedMethodsIncludesOperationalBundles(t *testing.T) {
	required := []string{
		MethodExecApprovalsGet,
		MethodExecApprovalsSet,
		MethodExecApprovalsNodeGet,
		MethodExecApprovalsNodeSet,
		MethodExecApprovalRequest,
		MethodExecApprovalWaitDecision,
		MethodExecApprovalResolve,
		MethodSecretsReload,
		MethodSecretsResolve,
		MethodWizardStart,
		MethodWizardNext,
		MethodWizardCancel,
		MethodWizardStatus,
		MethodUpdateRun,
		MethodTalkConfig,
		MethodTalkMode,
		MethodLastHeartbeat,
		MethodSetHeartbeats,
		MethodWake,
		MethodSystemPresence,
		MethodSystemEvent,
		MethodSend,
		MethodBrowserRequest,
		MethodVoicewakeGet,
		MethodVoicewakeSet,
		MethodTTSStatus,
		MethodTTSProviders,
		MethodTTSSetProvider,
		MethodTTSEnable,
		MethodTTSDisable,
		MethodTTSConvert,
	}
	for _, want := range required {
		found := false
		for _, method := range SupportedMethods() {
			if method == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not found in supported methods", want)
		}
	}
}

func TestNodeSurfaceDecodeAndNormalize(t *testing.T) {
	listReq, err := DecodeNodeListParams(json.RawMessage(`[10]`))
	if err != nil {
		t.Fatalf("node.list decode error: %v", err)
	}
	listReq, err = listReq.Normalize()
	if err != nil {
		t.Fatalf("node.list normalize error: %v", err)
	}
	if listReq.Limit != 10 {
		t.Fatalf("unexpected node.list request: %#v", listReq)
	}

	describeReq, err := DecodeNodeDescribeParams(json.RawMessage(`{"node_id":"n1"}`))
	if err != nil {
		t.Fatalf("node.describe decode error: %v", err)
	}
	if _, err := describeReq.Normalize(); err != nil {
		t.Fatalf("node.describe normalize error: %v", err)
	}

	renameReq, err := DecodeNodeRenameParams(json.RawMessage(`["n1","Kitchen Mac"]`))
	if err != nil {
		t.Fatalf("node.rename decode error: %v", err)
	}
	renameReq, err = renameReq.Normalize()
	if err != nil {
		t.Fatalf("node.rename normalize error: %v", err)
	}
	if renameReq.Name != "Kitchen Mac" {
		t.Fatalf("unexpected node.rename request: %#v", renameReq)
	}

	refreshReq, err := DecodeNodeCanvasCapabilityRefreshParams(json.RawMessage(`{"node_id":"n1"}`))
	if err != nil {
		t.Fatalf("node.canvas.capability.refresh decode error: %v", err)
	}
	if _, err := refreshReq.Normalize(); err != nil {
		t.Fatalf("node.canvas.capability.refresh normalize error: %v", err)
	}
}

func TestSupportedMethodsIncludesNodeSurfaceBundle(t *testing.T) {
	required := []string{MethodNodeList, MethodNodeDescribe, MethodNodeRename, MethodNodeInvokeResult, MethodNodeCanvasCapabilityRefresh}
	for _, want := range required {
		found := false
		for _, method := range SupportedMethods() {
			if method == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s not found in supported methods", want)
		}
	}
}

func TestRuntimeObserveRequestDecodeAndNormalize(t *testing.T) {
	req, err := DecodeRuntimeObserveParams(json.RawMessage(`{"includeEvents":true,"includeLogs":false,"eventCursor":3,"logCursor":4,"eventLimit":7,"logLimit":8,"maxBytes":2048,"waitTimeoutMs":25,"events":["tool.start","turn.result"],"agentId":" agent-1 ","sessionId":" sess-1 ","channelId":" ch-1 ","direction":" outbound ","subsystem":" tool ","source":" reply "}`))
	if err != nil {
		t.Fatalf("runtime.observe decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("runtime.observe normalize error: %v", err)
	}
	if req.IncludeEvents == nil || !*req.IncludeEvents {
		t.Fatalf("expected include_events to remain true: %#v", req)
	}
	if req.IncludeLogs == nil || *req.IncludeLogs {
		t.Fatalf("expected include_logs to remain false: %#v", req)
	}
	if req.AgentID != "agent-1" || req.SessionID != "sess-1" || req.ChannelID != "ch-1" {
		t.Fatalf("expected trimmed identity filters, got %#v", req)
	}
	if req.Direction != "outbound" || req.Subsystem != "tool" || req.Source != "reply" {
		t.Fatalf("expected trimmed routing filters, got %#v", req)
	}
	if req.EventLimit != 7 || req.LogLimit != 8 || req.MaxBytes != 2048 || req.WaitTimeoutMS != 25 {
		t.Fatalf("unexpected normalized bounds: %#v", req)
	}
	if len(req.Events) != 2 || req.Events[0] != "tool.start" || req.Events[1] != "turn.result" {
		t.Fatalf("unexpected events filter list: %#v", req.Events)
	}

	invalid, err := DecodeRuntimeObserveParams(json.RawMessage(`{"include_events":false,"include_logs":false}`))
	if err != nil {
		t.Fatalf("runtime.observe invalid decode error: %v", err)
	}
	if _, err := invalid.Normalize(); err == nil {
		t.Fatal("expected normalize error when both include flags are false")
	}
}

func TestSupportedMethodsIncludesRuntimeObserve(t *testing.T) {
	for _, method := range SupportedMethods() {
		if method == MethodRuntimeObserve {
			return
		}
	}
	t.Fatalf("%s not found in supported methods", MethodRuntimeObserve)
}

// ── Schema parity: new AgentRequest fields ──────────────────────────────────

func TestDecodeAgentParams_NewFields(t *testing.T) {
	raw := json.RawMessage(`{
		"message": "hello",
		"session_id": "sess-1",
		"sessionKey": "key-1",
		"agent_id": "agent-1",
		"to": "npub1abc",
		"reply_to": "npub1def",
		"thinking": "chain",
		"deliver": true,
		"channel": "webchat",
		"reply_channel": "nostr",
		"account_id": "acc-1",
		"reply_account_id": "acc-2",
		"thread_id": "t-1",
		"group_id": "g-1",
		"group_channel": "gc-1",
		"group_space": "gs-1",
		"best_effort_deliver": false,
		"lane": "fast",
		"extra_system_prompt": "Be brief.",
		"idempotency_key": "idem-1",
		"label": "test-label"
	}`)

	req, err := DecodeAgentParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.SessionKey != "key-1" {
		t.Errorf("session_key = %q", req.SessionKey)
	}
	if req.AgentID != "agent-1" {
		t.Errorf("agent_id = %q", req.AgentID)
	}
	if req.To != "npub1abc" {
		t.Errorf("to = %q", req.To)
	}
	if req.ReplyTo != "npub1def" {
		t.Errorf("reply_to = %q", req.ReplyTo)
	}
	if req.Thinking != "chain" {
		t.Errorf("thinking = %q", req.Thinking)
	}
	if req.Deliver == nil || !*req.Deliver {
		t.Errorf("deliver = %v", req.Deliver)
	}
	if req.Channel != "webchat" {
		t.Errorf("channel = %q", req.Channel)
	}
	if req.ThreadID != "t-1" {
		t.Errorf("thread_id = %q", req.ThreadID)
	}
	if req.GroupID != "g-1" {
		t.Errorf("group_id = %q", req.GroupID)
	}
	if req.GroupChannel != "gc-1" {
		t.Errorf("group_channel = %q", req.GroupChannel)
	}
	if req.GroupSpace != "gs-1" {
		t.Errorf("group_space = %q", req.GroupSpace)
	}
	if req.BestEffortDeliver == nil || *req.BestEffortDeliver {
		t.Errorf("best_effort_deliver = %v", req.BestEffortDeliver)
	}
	if req.Lane != "fast" {
		t.Errorf("lane = %q", req.Lane)
	}
	if req.ExtraSystemPrompt != "Be brief." {
		t.Errorf("extra_system_prompt = %q", req.ExtraSystemPrompt)
	}
	if req.IdempotencyKey != "idem-1" {
		t.Errorf("idempotency_key = %q", req.IdempotencyKey)
	}
	if req.Label != "test-label" {
		t.Errorf("label = %q", req.Label)
	}
}

func TestDecodeAgentParams_CamelCaseNewFields(t *testing.T) {
	raw := json.RawMessage(`{
		"message": "hello",
		"replyTo": "npub1def",
		"threadId": "t-2",
		"groupId": "g-2",
		"groupChannel": "gc-2",
		"groupSpace": "gs-2",
		"bestEffortDeliver": true,
		"extraSystemPrompt": "Extra",
		"inputProvenance": {"kind":"dm","source_channel":"nostr"}
	}`)

	req, err := DecodeAgentParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.ReplyTo != "npub1def" {
		t.Errorf("reply_to = %q", req.ReplyTo)
	}
	if req.ThreadID != "t-2" {
		t.Errorf("thread_id = %q", req.ThreadID)
	}
	if req.GroupID != "g-2" {
		t.Errorf("group_id = %q", req.GroupID)
	}
	if req.GroupSpace != "gs-2" {
		t.Errorf("group_space = %q", req.GroupSpace)
	}
	if req.BestEffortDeliver == nil || !*req.BestEffortDeliver {
		t.Errorf("best_effort_deliver = %v", req.BestEffortDeliver)
	}
	if req.ExtraSystemPrompt != "Extra" {
		t.Errorf("extra_system_prompt = %q", req.ExtraSystemPrompt)
	}
	if req.InputProvenance == nil || req.InputProvenance.Kind != "dm" {
		t.Errorf("input_provenance = %+v", req.InputProvenance)
	}
}

func TestDecodeAgentParams_InternalEvents(t *testing.T) {
	raw := json.RawMessage(`{
		"message": "process events",
		"internal_events": [
			{
				"type": "task_completion",
				"source": "subagent",
				"child_session_key": "child-1",
				"announce_type": "result",
				"task_label": "research",
				"status": "ok",
				"status_label": "Completed",
				"result": "42",
				"reply_instruction": "summarize"
			}
		]
	}`)

	req, err := DecodeAgentParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if len(req.InternalEvents) != 1 {
		t.Fatalf("expected 1 internal event, got %d", len(req.InternalEvents))
	}
	evt := req.InternalEvents[0]
	if evt.Type != "task_completion" || evt.Source != "subagent" || evt.Status != "ok" {
		t.Errorf("internal event = %+v", evt)
	}
	if evt.ChildSessionKey != "child-1" || evt.TaskLabel != "research" || evt.Result != "42" {
		t.Errorf("internal event fields = %+v", evt)
	}
}

// ── Schema parity: AgentIdentityRequest SessionKey ──────────────────────────

func TestDecodeAgentIdentityParams_SessionKey(t *testing.T) {
	raw := json.RawMessage(`{"agent_id":"main","sessionKey":"sk-1"}`)
	req, err := DecodeAgentIdentityParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.SessionKey != "sk-1" {
		t.Errorf("session_key = %q", req.SessionKey)
	}
}

// ── Schema parity: AgentsCreate/Update/Delete new fields ────────────────────

func TestDecodeAgentsCreateParams_EmojiAvatar(t *testing.T) {
	raw := json.RawMessage(`{"agent_id":"bot-1","name":"Bot","workspace":"/tmp","emoji":"🤖","avatar":"https://img/bot.png"}`)
	req, err := DecodeAgentsCreateParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.Emoji != "🤖" || req.Avatar != "https://img/bot.png" {
		t.Errorf("emoji=%q avatar=%q", req.Emoji, req.Avatar)
	}
}

func TestDecodeAgentsUpdateParams_Avatar(t *testing.T) {
	raw := json.RawMessage(`{"agent_id":"bot-1","avatar":"https://img/new.png"}`)
	req, err := DecodeAgentsUpdateParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.Avatar != "https://img/new.png" {
		t.Errorf("avatar=%q", req.Avatar)
	}
}

func TestDecodeAgentsDeleteParams_DeleteFiles(t *testing.T) {
	raw := json.RawMessage(`{"agent_id":"bot-1","delete_files":true}`)
	req, err := DecodeAgentsDeleteParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.DeleteFiles == nil || !*req.DeleteFiles {
		t.Errorf("delete_files = %v", req.DeleteFiles)
	}
}

// ── Schema parity: SendRequest new fields ───────────────────────────────────

func TestDecodeSendParams_NewFields(t *testing.T) {
	raw := json.RawMessage(`{
		"to":"0000000000000000000000000000000000000000000000000000000000000001",
		"message":"hello",
		"gif_playback":true,
		"account_id":"acc-1",
		"agent_id":"main",
		"thread_id":"t-1",
		"sessionKey":"sk-1",
		"idempotencyKey":"idem-1"
	}`)
	req, err := DecodeSendParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.GifPlayback == nil || !*req.GifPlayback {
		t.Errorf("gif_playback = %v", req.GifPlayback)
	}
	if req.AccountID != "acc-1" {
		t.Errorf("account_id = %q", req.AccountID)
	}
	if req.AgentID != "main" {
		t.Errorf("agent_id = %q", req.AgentID)
	}
	if req.ThreadID != "t-1" {
		t.Errorf("thread_id = %q", req.ThreadID)
	}
	if req.SessionKey != "sk-1" {
		t.Errorf("session_key = %q", req.SessionKey)
	}
}

// ── Schema parity: ChannelsLogoutRequest AccountID ──────────────────────────

func TestDecodeChannelsLogoutParams_AccountID(t *testing.T) {
	raw := json.RawMessage(`{"channel":"telegram","account_id":"acc-1"}`)
	req, err := DecodeChannelsLogoutParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.AccountID != "acc-1" {
		t.Errorf("account_id = %q", req.AccountID)
	}
}

// ── Schema parity: ExecApprovalRequest new fields ───────────────────────────

func TestDecodeExecApprovalRequestParams_NewFields(t *testing.T) {
	raw := json.RawMessage(`{
		"id":"approval-42",
		"command":"npm install",
		"command_argv":["npm","install"],
		"env":{"NODE_ENV":"production"},
		"cwd":"/app",
		"host":"localhost",
		"security":"sandboxed",
		"ask":"auto",
		"agent_id":"main",
		"resolved_path":"/usr/bin/npm",
		"sessionKey":"sk-1",
		"turn_source_channel":"webchat",
		"turn_source_to":"user-1",
		"turn_source_account_id":"acc-1",
		"turn_source_thread_id":"thread-1",
		"two_phase":true,
		"timeout_ms":30000,
		"system_run_plan":{
			"argv":["npm","install"],
			"cwd":"/app",
			"commandText":"npm install",
			"agentId":"main",
			"sessionKey":"sk-1"
		}
	}`)
	req, err := DecodeExecApprovalRequestParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if req.ID != "approval-42" {
		t.Errorf("id = %q", req.ID)
	}
	if len(req.CommandArgv) != 2 || req.CommandArgv[0] != "npm" {
		t.Errorf("command_argv = %v", req.CommandArgv)
	}
	if req.Env["NODE_ENV"] != "production" {
		t.Errorf("env = %v", req.Env)
	}
	if req.CWD == nil || *req.CWD != "/app" {
		t.Errorf("cwd = %v", req.CWD)
	}
	if req.TwoPhase == nil || !*req.TwoPhase {
		t.Errorf("two_phase = %v", req.TwoPhase)
	}
	if req.SystemRunPlan == nil || req.SystemRunPlan.CommandText != "npm install" {
		t.Errorf("system_run_plan = %+v", req.SystemRunPlan)
	}
	if req.SystemRunPlan != nil && len(req.SystemRunPlan.Argv) != 2 {
		t.Errorf("system_run_plan.argv = %v", req.SystemRunPlan.Argv)
	}
}

func TestDecodeExecApprovalRequestParams_CamelCase(t *testing.T) {
	raw := json.RawMessage(`{
		"command":"ls",
		"commandArgv":["ls","-la"],
		"resolvedPath":"/bin/ls",
		"turnSourceChannel":"webchat",
		"twoPhase":false
	}`)
	req, err := DecodeExecApprovalRequestParams(raw)
	if err != nil {
		t.Fatalf("decode error: %v", err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatalf("normalize error: %v", err)
	}
	if len(req.CommandArgv) != 2 {
		t.Errorf("command_argv = %v", req.CommandArgv)
	}
	if req.ResolvedPath == nil || *req.ResolvedPath != "/bin/ls" {
		t.Errorf("resolved_path = %v", req.ResolvedPath)
	}
	if req.TurnSourceChannel == nil || *req.TurnSourceChannel != "webchat" {
		t.Errorf("turn_source_channel = %v", req.TurnSourceChannel)
	}
	if req.TwoPhase == nil || *req.TwoPhase {
		t.Errorf("two_phase = %v", req.TwoPhase)
	}
}
