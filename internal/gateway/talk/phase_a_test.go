package talk

import (
	"context"
	"errors"
	"testing"
)

func personaExtra() map[string]any {
	return map[string]any{
		"talk": map[string]any{
			"tts_provider": "fakeprov",
			"personas": []any{
				map[string]any{"id": "narrator", "name": "Narrator", "provider": "fakeprov", "voice": "alloy"},
				map[string]any{"id": "coach", "name": "Coach", "voice": "echo"},
			},
		},
	}
}

func TestListPersonas(t *testing.T) {
	personas, active := ListPersonas(personaExtra(), "coach")
	if len(personas) != 2 {
		t.Fatalf("want 2 personas, got %d", len(personas))
	}
	// Sorted by id: coach, narrator.
	if personas[0].ID != "coach" || personas[1].ID != "narrator" {
		t.Fatalf("unexpected order: %+v", personas)
	}
	if active != "coach" {
		t.Fatalf("want active=coach, got %q", active)
	}
	// Unknown active resolves to empty.
	if _, a := ListPersonas(personaExtra(), "ghost"); a != "" {
		t.Fatalf("want empty active for unknown persona, got %q", a)
	}
	// Empty config yields no personas.
	if p, _ := ListPersonas(nil, ""); len(p) != 0 {
		t.Fatalf("want 0 personas for nil extra, got %d", len(p))
	}
}

func TestValidatePersona(t *testing.T) {
	extra := personaExtra()
	for _, clear := range []string{"off", "none", "default", ""} {
		resolved, cleared, err := ValidatePersona(extra, clear)
		if err != nil || !cleared || resolved != "" {
			t.Fatalf("clear alias %q: resolved=%q cleared=%v err=%v", clear, resolved, cleared, err)
		}
	}
	resolved, cleared, err := ValidatePersona(extra, "NARRATOR")
	if err != nil || cleared || resolved != "narrator" {
		t.Fatalf("case-insensitive match: resolved=%q cleared=%v err=%v", resolved, cleared, err)
	}
	if _, _, err := ValidatePersona(extra, "ghost"); err == nil {
		t.Fatal("want error for unknown persona")
	}
}

func TestBuildCatalog(t *testing.T) {
	in := CatalogInput{
		SpeechProviders: []map[string]any{
			{"id": "openai", "configured": true},
			{"id": "kokoro", "configured": false},
		},
		ActiveSpeech: "openai",
	}
	cat := BuildCatalog(in)
	speech := cat["speech"].(map[string]any)
	if speech["ready"] != true {
		t.Fatal("speech should be ready when a provider is configured")
	}
	if speech["active"] != "openai" {
		t.Fatalf("want active openai, got %v", speech["active"])
	}
	// Transcription/realtime honestly not ready with empty lists.
	if cat["transcription"].(map[string]any)["ready"] != false {
		t.Fatal("transcription should be not-ready while unwired")
	}
	if cat["realtime"].(map[string]any)["ready"] != false {
		t.Fatal("realtime should be not-ready while unwired")
	}
	transports := cat["transports"].([]transportDescriptor)
	var relay, managed *transportDescriptor
	for i := range transports {
		switch transports[i].ID {
		case "gateway-relay":
			relay = &transports[i]
		case "managed-room":
			managed = &transports[i]
		}
	}
	if relay == nil || !relay.Ready {
		t.Fatal("gateway-relay must be ready")
	}
	if managed == nil || managed.Ready {
		t.Fatal("managed-room must be honestly not-ready")
	}
}

func TestSpeakSuccess(t *testing.T) {
	mgr := ttsManagerWith(&fakeTTSProvider{id: "fakeprov", configured: true, format: "mp3"})
	out, err := Speak(context.Background(), mgr, personaExtra(), SpeakRequest{Text: "hello", Persona: "narrator"})
	if err != nil {
		t.Fatalf("speak: %v", err)
	}
	if out.Provider != "fakeprov" {
		t.Fatalf("want provider fakeprov, got %q", out.Provider)
	}
	if out.OutputFormat != "mp3" || out.MimeType != "audio/mpeg" {
		t.Fatalf("unexpected format/mime: %q / %q", out.OutputFormat, out.MimeType)
	}
	if out.AudioBase64 == "" {
		t.Fatal("want inline audio base64")
	}
	if out.Persona != "narrator" {
		t.Fatalf("want persona narrator, got %q", out.Persona)
	}
}

func TestSpeakStructuredErrors(t *testing.T) {
	extra := personaExtra()

	// nil manager -> talk_unconfigured
	if _, err := Speak(context.Background(), nil, extra, SpeakRequest{Text: "hi"}); reasonOf(err) != ReasonTalkUnconfigured {
		t.Fatalf("nil manager: want talk_unconfigured, got %v", err)
	}

	// unconfigured provider -> talk_unconfigured
	mgrUnconf := ttsManagerWith(&fakeTTSProvider{id: "fakeprov", configured: false, format: "mp3"})
	if _, err := Speak(context.Background(), mgrUnconf, extra, SpeakRequest{Text: "hi", Provider: "fakeprov"}); reasonOf(err) != ReasonTalkUnconfigured {
		t.Fatalf("unconfigured: want talk_unconfigured, got %v", err)
	}

	// synthesis failure -> synthesis_failed
	mgrFail := ttsManagerWith(&fakeTTSProvider{id: "fakeprov", configured: true, format: "mp3", fail: errors.New("boom")})
	if _, err := Speak(context.Background(), mgrFail, extra, SpeakRequest{Text: "hi", Provider: "fakeprov"}); reasonOf(err) != ReasonSynthesisFailed {
		t.Fatalf("synthesis: want synthesis_failed, got %v", err)
	}

	// empty audio -> invalid_audio_result
	mgrEmpty := ttsManagerWith(&fakeTTSProvider{id: "fakeprov", configured: true, format: "mp3", empty: true})
	if _, err := Speak(context.Background(), mgrEmpty, extra, SpeakRequest{Text: "hi", Provider: "fakeprov"}); reasonOf(err) != ReasonInvalidAudioResult {
		t.Fatalf("empty: want invalid_audio_result, got %v", err)
	}

	// unknown persona -> talk_unconfigured
	mgrOK := ttsManagerWith(&fakeTTSProvider{id: "fakeprov", configured: true, format: "mp3"})
	if _, err := Speak(context.Background(), mgrOK, extra, SpeakRequest{Text: "hi", Persona: "ghost"}); reasonOf(err) != ReasonTalkUnconfigured {
		t.Fatalf("unknown persona: want talk_unconfigured, got %v", err)
	}
}

func TestVoiceAliasResolution(t *testing.T) {
	extra := map[string]any{"talk": map[string]any{
		"voice_aliases": map[string]any{"friendly": "nova"},
	}}
	got := resolveVoiceAlias(talkConfig(extra), "friendly")
	if got != "nova" {
		t.Fatalf("want nova, got %q", got)
	}
	if resolveVoiceAlias(talkConfig(extra), "unmapped") != "unmapped" {
		t.Fatal("unmapped voice should pass through")
	}
}

func TestRoutingStoreSetGet(t *testing.T) {
	s := NewRoutingStore()
	if s.Get().Version != 0 {
		t.Fatal("initial version should be 0")
	}
	stored, err := s.Set(RoutingConfig{
		DefaultTarget: "main",
		Routes: []RoutingRoute{
			{Trigger: "  Hey  Metiq ", Target: "agent-a"},
			{Trigger: "yo", Target: "agent-b", Mode: "push"},
		},
	})
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	if stored.Version != 1 {
		t.Fatalf("want version 1, got %d", stored.Version)
	}
	if stored.Routes[0].Trigger != "hey metiq" {
		t.Fatalf("trigger not normalized: %q", stored.Routes[0].Trigger)
	}
	// Second set bumps version.
	stored2, _ := s.Set(RoutingConfig{Routes: []RoutingRoute{}})
	if stored2.Version != 2 {
		t.Fatalf("want version 2, got %d", stored2.Version)
	}
	if s.Get().Version != 2 {
		t.Fatal("get should reflect latest version")
	}
}

func TestNormalizeRoutingValidation(t *testing.T) {
	// Duplicate triggers rejected.
	if _, err := NormalizeRouting(RoutingConfig{Routes: []RoutingRoute{
		{Trigger: "hey", Target: "a"}, {Trigger: "HEY", Target: "b"},
	}}); err == nil {
		t.Fatal("want duplicate trigger error")
	}
	// Missing target rejected.
	if _, err := NormalizeRouting(RoutingConfig{Routes: []RoutingRoute{{Trigger: "hey"}}}); err == nil {
		t.Fatal("want missing target error")
	}
	// Empty trigger rejected.
	if _, err := NormalizeRouting(RoutingConfig{Routes: []RoutingRoute{{Trigger: "   ", Target: "a"}}}); err == nil {
		t.Fatal("want empty trigger error")
	}
	// Over the cap rejected.
	routes := make([]RoutingRoute, MaxVoicewakeRoutes+1)
	for i := range routes {
		routes[i] = RoutingRoute{Trigger: string(rune('a'+i%26)) + string(rune('0'+i/26)), Target: "t"}
	}
	if _, err := NormalizeRouting(RoutingConfig{Routes: routes}); err == nil {
		t.Fatal("want route-cap error")
	}
}

func reasonOf(err error) string {
	var re *ReasonedError
	if errors.As(err, &re) {
		return re.Reason
	}
	return ""
}
