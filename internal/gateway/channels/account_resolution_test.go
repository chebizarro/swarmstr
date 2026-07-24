package channels

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
	"metiq/internal/store/state"
)

func TestResolveChannelAccountParamsNamedAndDefault(t *testing.T) {
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"work": {
			Kind: "slack",
			Config: map[string]any{
				"bot_token":       "xoxb-work",
				"channel_id":      "CWORK",
				"default_account": true,
			},
		},
		"alerts": {
			Kind: "slack",
			Config: map[string]any{
				"bot_token":  "xoxb-alerts",
				"channel_id": "CALERTS",
			},
		},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })

	input := map[string]any{"text": "hello"}
	resolved, err := ResolveChannelAccountParams("slack", input)
	if err != nil {
		t.Fatalf("resolve default: %v", err)
	}
	if resolved["account_id"] != "work" || resolved["bot_token"] != "xoxb-work" || resolved["channel_id"] != "CWORK" {
		t.Fatalf("unexpected default resolution: %#v", resolved)
	}
	if _, mutated := input["bot_token"]; mutated {
		t.Fatal("resolver mutated caller params")
	}

	resolved, err = ResolveChannelAccountParams("slack", map[string]any{
		"account_id": "alerts",
		"account":    "+15551234567", // provider field, not an account selector
		"channel_id": "COVERRIDE",
		"text":       "warning",
	})
	if err != nil {
		t.Fatalf("resolve named: %v", err)
	}
	if resolved["bot_token"] != "xoxb-alerts" || resolved["channel_id"] != "COVERRIDE" || resolved["account_id"] != "alerts" || resolved["account"] != "+15551234567" {
		t.Fatalf("unexpected named resolution: %#v", resolved)
	}
}

func TestResolveChannelAccountParamsErrorsAreUseful(t *testing.T) {
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"one": {Kind: "telegram", Config: map[string]any{"token": "one"}},
		"two": {Kind: "telegram", Config: map[string]any{"token": "two"}},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })

	if _, err := ResolveChannelAccountParams("telegram", map[string]any{"text": "hello"}); err == nil || !strings.Contains(err.Error(), "account is required") || !strings.Contains(err.Error(), "one, two") {
		t.Fatalf("expected deterministic ambiguous-account error, got %v", err)
	}
	if _, err := ResolveChannelAccountParams("telegram", map[string]any{"account_id": "missing"}); err == nil || !strings.Contains(err.Error(), "not configured") || !strings.Contains(err.Error(), "one, two") {
		t.Fatalf("expected useful missing-account error, got %v", err)
	}
}

func TestResolveChannelAccountParamsLegacyWithoutConfiguredAccounts(t *testing.T) {
	ConfigureChannelAccounts(nil)
	input := map[string]any{"token": "legacy", "chat_id": "42"}
	resolved, err := ResolveChannelAccountParams("telegram", input)
	if err != nil {
		t.Fatalf("legacy resolution: %v", err)
	}
	resolved["token"] = "changed"
	if input["token"] != "legacy" {
		t.Fatal("legacy resolution did not copy caller params")
	}
}

func TestAccountScopedGatewayMethodsInjectsCredentialsWithoutReturningThem(t *testing.T) {
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"default": {Kind: "telegram", Config: map[string]any{"token": "secret-token"}},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })

	var received map[string]any
	methods := AccountScopedGatewayMethods("telegram", []sdk.GatewayMethod{{
		Method: "telegram.test",
		Handle: func(_ context.Context, params map[string]any) (map[string]any, error) {
			received = params
			return map[string]any{"ok": true, "account_id": params["account_id"]}, nil
		},
	}})
	result, err := methods[0].Handle(context.Background(), map[string]any{"text": "hello"})
	if err != nil {
		t.Fatalf("wrapped handle: %v", err)
	}
	if received["token"] != "secret-token" || received["account_id"] != "default" {
		t.Fatalf("handler did not receive resolved account: %#v", received)
	}
	if _, leaked := result["token"]; leaked {
		t.Fatalf("gateway result leaked credentials: %#v", result)
	}
}

func TestConfigureChannelAccountsNormalizesNextcloudAlias(t *testing.T) {
	ConfigureChannelAccounts(state.NostrChannelsConfig{
		"default": {Kind: "nextcloud", Config: map[string]any{"token": "secret"}},
	})
	t.Cleanup(func() { ConfigureChannelAccounts(nil) })
	resolved, err := ResolveChannelAccountParams("nextcloud-talk", nil)
	if err != nil {
		t.Fatalf("resolve alias: %v", err)
	}
	if resolved["token"] != "secret" {
		t.Fatalf("alias did not resolve configured account: %#v", resolved)
	}
}
