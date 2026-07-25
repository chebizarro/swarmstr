package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"metiq/internal/gateway/methods"
	userprofilespkg "metiq/internal/gateway/userprofiles"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func usersCall(t *testing.T, h controlRPCHandler, method, params, fromPubKey string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleUsersRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method:     method,
		Params:     json.RawMessage(params),
		FromPubKey: fromPubKey,
	}, method, state.ConfigDoc{})
	if !handled {
		t.Fatalf("method %s was not handled by users dispatch", method)
	}
	return result, err
}

func TestUsersRPCStoreUnavailable(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	if _, err := usersCall(t, h, methods.MethodUsersList, `{}`, ""); err == nil {
		t.Fatal("expected error when user profile store is nil")
	}
}

func TestUsersSelfRequiresIdentity(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{userProfiles: userprofilespkg.NewManager()})
	if _, err := usersCall(t, h, methods.MethodUsersSelf, `{}`, ""); err == nil {
		t.Fatal("expected error when caller has no pubkey")
	}
}

func TestUsersLifecycle(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{userProfiles: userprofilespkg.NewManager()})

	// self creates the caller's profile.
	res, err := usersCall(t, h, methods.MethodUsersSelf, `{}`, "pubkey-caller")
	if err != nil {
		t.Fatalf("users.self: %v", err)
	}
	profile := res.Result.(map[string]any)["profile"].(userprofilespkg.Profile)
	if profile.ID != "pubkey-caller" {
		t.Fatalf("unexpected self profile: %+v", profile)
	}

	// list contains it.
	res, err = usersCall(t, h, methods.MethodUsersList, `{}`, "pubkey-caller")
	if err != nil {
		t.Fatalf("users.list: %v", err)
	}
	if got := len(res.Result.(map[string]any)["profiles"].([]userprofilespkg.Profile)); got != 1 {
		t.Fatalf("expected 1 profile, got %d", got)
	}

	// linkEmail to a missing profile is an error.
	if _, err := usersCall(t, h, methods.MethodUsersLinkEmail,
		`{"email":"a@b.c","targetProfileId":"nope"}`, "pubkey-caller"); err == nil {
		t.Fatal("expected not-found error for missing target profile")
	}

	// linkEmail to the caller's profile.
	res, err = usersCall(t, h, methods.MethodUsersLinkEmail,
		`{"email":"caller@example.com","targetProfileId":"pubkey-caller"}`, "pubkey-caller")
	if err != nil {
		t.Fatalf("users.linkEmail: %v", err)
	}
	profile = res.Result.(map[string]any)["profile"].(userprofilespkg.Profile)
	if len(profile.Emails) != 1 || profile.Emails[0] != "caller@example.com" {
		t.Fatalf("unexpected emails: %+v", profile.Emails)
	}

	// setDisplayName requires the field; explicit null clears it.
	if _, err := usersCall(t, h, methods.MethodUsersSetDisplayName,
		`{"profileId":"pubkey-caller"}`, "pubkey-caller"); err == nil {
		t.Fatal("expected error when displayName omitted")
	}
	res, err = usersCall(t, h, methods.MethodUsersSetDisplayName,
		`{"profileId":"pubkey-caller","displayName":"Caller"}`, "pubkey-caller")
	if err != nil {
		t.Fatalf("users.setDisplayName: %v", err)
	}
	profile = res.Result.(map[string]any)["profile"].(userprofilespkg.Profile)
	if profile.DisplayName == nil || *profile.DisplayName != "Caller" {
		t.Fatalf("unexpected display name: %+v", profile.DisplayName)
	}

	// setAvatar with valid base64.
	avatar := base64.StdEncoding.EncodeToString([]byte{0x89, 0x50, 0x4e, 0x47})
	res, err = usersCall(t, h, methods.MethodUsersSetAvatar,
		`{"profileId":"pubkey-caller","mime":"image/png","avatarBase64":"`+avatar+`"}`, "pubkey-caller")
	if err != nil {
		t.Fatalf("users.setAvatar: %v", err)
	}
	profile = res.Result.(map[string]any)["profile"].(userprofilespkg.Profile)
	if !profile.HasAvatar || profile.AvatarMime == nil || *profile.AvatarMime != "image/png" {
		t.Fatalf("unexpected avatar projection: %+v", profile)
	}

	// setAvatar with invalid base64 is rejected.
	if _, err := usersCall(t, h, methods.MethodUsersSetAvatar,
		`{"profileId":"pubkey-caller","mime":"image/png","avatarBase64":"@@@notbase64@@@"}`, "pubkey-caller"); err == nil {
		t.Fatal("expected invalid base64 error")
	}
}
