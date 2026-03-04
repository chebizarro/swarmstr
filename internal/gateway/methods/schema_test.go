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
	if req.ExpectedVersion != 2 || req.ExpectedEvent != "abc" {
		t.Fatalf("unexpected precondition: %+v", req)
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
	if req.ExpectedVersion != 3 || req.ExpectedEvent != "evt-1" {
		t.Fatalf("unexpected precondition: %+v", req)
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
	required := []string{MethodModelsList, MethodToolsCatalog, MethodSkillsStatus, MethodSkillsInstall, MethodSkillsUpdate}
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
