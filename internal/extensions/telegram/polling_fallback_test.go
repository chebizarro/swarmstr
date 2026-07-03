package telegram

import (
	"context"
	"strings"
	"testing"

	"metiq/internal/plugins/sdk"
)

// TestConnectRefusesImplicitPolling asserts that, without a webhook_url, Connect
// refuses to silently start a long-polling loop. Polling is a non-event-driven
// dev fallback that must be explicitly opted into via allow_polling.
func TestConnectRefusesImplicitPolling(t *testing.T) {
	p := &TelegramPlugin{}
	_, err := p.Connect(context.Background(), "telegram-main", map[string]any{
		"token": "token",
	}, func(sdk.InboundChannelMessage) {})
	if err == nil {
		t.Fatal("expected Connect to refuse implicit polling without webhook_url or allow_polling")
	}
	if !strings.Contains(err.Error(), "allow_polling") {
		t.Fatalf("expected error to mention allow_polling fallback, got: %v", err)
	}
}

// TestConfigSchemaAdvertisesPollingFallback ensures the explicit dev-fallback
// flag is documented in the config schema.
func TestConfigSchemaAdvertisesPollingFallback(t *testing.T) {
	schema := (&TelegramPlugin{}).ConfigSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties map in schema")
	}
	if _, ok := props["allow_polling"]; !ok {
		t.Fatal("expected allow_polling key in Telegram config schema")
	}
}
