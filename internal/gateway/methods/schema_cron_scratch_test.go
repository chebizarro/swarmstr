package methods

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeCronJobSelectorAliases(t *testing.T) {
	for _, raw := range []string{`{"id":"job-1"}`, `{"jobId":"job-1"}`, `{"id":"job-1","jobId":"job-1"}`} {
		req, err := DecodeCronGetParams(json.RawMessage(raw))
		if err != nil {
			t.Fatalf("DecodeCronGetParams(%s): %v", raw, err)
		}
		req, err = req.Normalize()
		if err != nil {
			t.Fatalf("Normalize(%s): %v", raw, err)
		}
		if req.ID != "job-1" {
			t.Fatalf("Normalize(%s) ID = %q, want job-1", raw, req.ID)
		}
	}

	for _, raw := range []string{`{}`, `{"id":"a","jobId":"b"}`, `{"jobId":"job-1","extra":true}`} {
		req, err := DecodeCronGetParams(json.RawMessage(raw))
		if err == nil {
			_, err = req.Normalize()
		}
		if err == nil {
			t.Fatalf("Decode/Normalize(%s) unexpectedly succeeded", raw)
		}
	}
}

func TestDecodeCronScratchSetContentAndCAS(t *testing.T) {
	req, err := DecodeCronScratchSetParams(json.RawMessage(`{"jobId":"job-1","content":"","expectedRevision":0}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err = req.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if req.ID != "job-1" || req.ContentValue != "" || req.Clear || req.ExpectedRevision == nil || *req.ExpectedRevision != 0 {
		t.Fatalf("unexpected normalized request: %+v", req)
	}

	clearReq, err := DecodeCronScratchSetParams(json.RawMessage(`{"id":"job-1","content":null}`))
	if err != nil {
		t.Fatal(err)
	}
	clearReq, err = clearReq.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if !clearReq.Clear {
		t.Fatal("null content did not normalize to clear")
	}
}

func TestDecodeCronScratchSetRejectsInvalidContent(t *testing.T) {
	tooLarge, _ := json.Marshal(map[string]any{"id": "job-1", "content": strings.Repeat("x", 262145)})
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"id":"job-1"}`),
		json.RawMessage(`{"id":"job-1","content":42}`),
		json.RawMessage(`{"id":"job-1","content":"x","expectedRevision":-1}`),
		tooLarge,
	} {
		req, err := DecodeCronScratchSetParams(raw)
		if err == nil {
			_, err = req.Normalize()
		}
		if err == nil {
			t.Fatalf("Decode/Normalize unexpectedly succeeded for %s", raw)
		}
	}
}
