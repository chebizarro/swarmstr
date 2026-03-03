package methods

import (
	"encoding/json"
	"testing"
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
