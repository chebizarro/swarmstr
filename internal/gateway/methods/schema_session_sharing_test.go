package methods

import (
	"encoding/json"
	"testing"
)

func TestDecodeSessionSharingParams(t *testing.T) {
	visibility, err := DecodeSessionVisibilitySetParams(json.RawMessage(`{"sessionKey":" s1 ","visibility":"read-only"}`))
	if err != nil {
		t.Fatal(err)
	}
	if visibility, err = visibility.Normalize(); err != nil || visibility.SessionKey != "s1" || visibility.Visibility != "read-only" {
		t.Fatalf("normalize: %+v err=%v", visibility, err)
	}
	if _, err := DecodeSessionVisibilitySetParams(json.RawMessage(`{"sessionKey":"s1","bogus":true}`)); err == nil {
		t.Fatal("unknown fields must be rejected")
	}
	member, err := DecodeSessionMemberMutateParams(json.RawMessage(`{"sessionKey":"s1","identityId":"bob"}`))
	if err != nil {
		t.Fatal(err)
	}
	if member, err = member.Normalize(); err != nil || member.IdentityID != "bob" {
		t.Fatalf("member normalize: %+v err=%v", member, err)
	}
	if bad := (SessionMemberMutateRequest{SessionKey: "s1"}); func() bool { _, err := bad.Normalize(); return err == nil }() {
		t.Fatal("identityId is required")
	}
	list, err := DecodeSessionMembersListParams(json.RawMessage(`{"sessionKey":"s1","agentId":"main"}`))
	if err != nil {
		t.Fatal(err)
	}
	if list, err = list.Normalize(); err != nil || list.AgentID != "main" {
		t.Fatalf("list normalize: %+v err=%v", list, err)
	}
	observer, err := DecodeSessionsObserverVisibilityParams(json.RawMessage(`{"visible":true}`))
	if err != nil || !observer.Visible {
		t.Fatalf("observer visibility decode: %+v err=%v", observer, err)
	}
}
