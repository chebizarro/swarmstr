package methods

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestSupportedMethodsIncludesAgentsMethods(t *testing.T) {
	required := []string{MethodAgentsList, MethodAgentsCreate, MethodAgentsUpdate, MethodAgentsDelete, MethodAgentsFilesList, MethodAgentsFilesGet, MethodAgentsFilesSet}
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

	installReq, err := DecodeSkillsInstallParams(json.RawMessage(`{"name":"nostr-core","install_id":"builtin"}`))
	if err != nil {
		t.Fatalf("skills.install decode error: %v", err)
	}
	installReq, err = installReq.Normalize()
	if err != nil {
		t.Fatalf("skills.install normalize error: %v", err)
	}
	if installReq.TimeoutMS <= 0 {
		t.Fatalf("expected normalized timeout, got: %#v", installReq)
	}

	updateReq, err := DecodeSkillsUpdateParams(json.RawMessage(`{"skill_key":"nostr-core","api_key":"  abc  ","env":{" K ":" V "}}`))
	if err != nil {
		t.Fatalf("skills.update decode error: %v", err)
	}
	updateReq, err = updateReq.Normalize()
	if err != nil {
		t.Fatalf("skills.update normalize error: %v", err)
	}
	if updateReq.APIKey == nil || *updateReq.APIKey != "abc" {
		t.Fatalf("unexpected api key normalization: %#v", updateReq)
	}
	if updateReq.Env["K"] != "V" {
		t.Fatalf("unexpected env normalization: %#v", updateReq.Env)
	}
}

func TestSupportedMethodsIncludesModelsToolsSkillsMethods(t *testing.T) {
	required := []string{MethodModelsList, MethodToolsCatalog, MethodSkillsStatus, MethodSkillsBins, MethodSkillsInstall, MethodSkillsUpdate}
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

	voicewakeReq, err := DecodeVoicewakeSetParams(json.RawMessage(`{"triggers":[" openclaw ","swarmstr"]}`))
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

	skillsBinsReq, err := DecodeSkillsBinsParams(json.RawMessage(`{"agent_id":"main"}`))
	if err != nil {
		t.Fatalf("skills.bins decode error: %v", err)
	}
	skillsBinsReq, err = skillsBinsReq.Normalize()
	if err != nil {
		t.Fatalf("skills.bins normalize error: %v", err)
	}
	if skillsBinsReq.AgentID != "main" {
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
