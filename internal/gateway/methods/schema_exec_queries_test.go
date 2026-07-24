package methods

import (
	"encoding/json"
	"testing"
)

func TestDecodeExecApprovalGetListAndUnifiedResolve(t *testing.T) {
	get, err := DecodeExecApprovalGetParams(json.RawMessage(`{"id":" approval-1 "}`))
	if err != nil {
		t.Fatal(err)
	}
	get, err = get.Normalize()
	if err != nil || get.ID != "approval-1" {
		t.Fatalf("get=%+v err=%v", get, err)
	}
	if _, err := DecodeExecApprovalListParams(json.RawMessage(`{"unexpected":true}`)); err == nil {
		t.Fatal("expected strict list params error")
	}
	resolve, err := DecodeApprovalResolveParams(json.RawMessage(`{"id":"approval-1","kind":"plugin","decision":"allow-once"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolve.Normalize(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"id":"approval-1","kind":"unknown","decision":"deny"}`,
		`{"id":"approval-1","kind":"exec","decision":"approve"}`,
	} {
		req, err := DecodeApprovalResolveParams(json.RawMessage(raw))
		if err == nil {
			_, err = req.Normalize()
		}
		if err == nil {
			t.Fatalf("expected validation error for %s", raw)
		}
	}
}
