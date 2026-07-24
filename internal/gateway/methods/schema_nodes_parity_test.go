package methods

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNodeParitySchemas(t *testing.T) {
	remove, err := DecodeNodePairRemoveParams(json.RawMessage(`{"node_id":"node-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if remove, err = remove.Normalize(); err != nil || remove.NodeID != "node-1" {
		t.Fatalf("node remove = (%+v, %v)", remove, err)
	}

	rename, err := DecodeDevicePairRenameParams(json.RawMessage(`{"device_id":"dev-1","label":"Desk"}`))
	if err != nil {
		t.Fatal(err)
	}
	if rename, err = rename.Normalize(); err != nil || rename.Label != "Desk" {
		t.Fatalf("device rename = (%+v, %v)", rename, err)
	}

	progress, err := DecodeNodeInvokeProgressParams(json.RawMessage(`{"invoke_id":"run-1","node_id":"node-1","seq":0,"chunk":"ok"}`))
	if err != nil {
		t.Fatal(err)
	}
	if progress, err = progress.Normalize(); err != nil || progress.InvokeID != "run-1" {
		t.Fatalf("progress = (%+v, %v)", progress, err)
	}
}

func TestNodeParitySchemasRejectInvalidValues(t *testing.T) {
	longLabel, _ := json.Marshal(map[string]any{"device_id": "dev-1", "label": strings.Repeat("x", 65)})
	tooLarge, _ := json.Marshal(map[string]any{"invoke_id": "run-1", "node_id": "node-1", "seq": 0, "chunk": strings.Repeat("x", 16*1024+1)})
	for _, tc := range []struct {
		raw       json.RawMessage
		normalize func(json.RawMessage) error
	}{
		{json.RawMessage(`{"node_id":""}`), func(raw json.RawMessage) error {
			r, err := DecodeNodePairRemoveParams(raw)
			if err != nil {
				return err
			}
			_, err = r.Normalize()
			return err
		}},
		{longLabel, func(raw json.RawMessage) error {
			r, err := DecodeDevicePairRenameParams(raw)
			if err != nil {
				return err
			}
			_, err = r.Normalize()
			return err
		}},
		{json.RawMessage(`{"invoke_id":"run-1","node_id":"node-1","seq":-1,"chunk":""}`), func(raw json.RawMessage) error {
			r, err := DecodeNodeInvokeProgressParams(raw)
			if err != nil {
				return err
			}
			_, err = r.Normalize()
			return err
		}},
		{tooLarge, func(raw json.RawMessage) error {
			r, err := DecodeNodeInvokeProgressParams(raw)
			if err != nil {
				return err
			}
			_, err = r.Normalize()
			return err
		}},
	} {
		if err := tc.normalize(tc.raw); err == nil {
			t.Fatalf("invalid params unexpectedly accepted: %s", tc.raw)
		}
	}
}
