package channels

import "testing"

func TestPollingFallbackEnabledRequiresExplicitOptIn(t *testing.T) {
	for _, cfg := range []map[string]any{
		nil,
		{},
		{"allow_polling": false},
		{"allow_polling": "true"},
		{"poll_interval_s": 5},
	} {
		if PollingFallbackEnabled(cfg) {
			t.Fatalf("polling unexpectedly enabled for %#v", cfg)
		}
	}
	if !PollingFallbackEnabled(map[string]any{"allow_polling": true}) {
		t.Fatal("explicit allow_polling=true did not enable fallback")
	}
}
