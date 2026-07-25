package talk

// Talk session modes advertised by talk.catalog. gateway-relay realtime,
// transcription-only, and the stt→brain→tts pipeline are the metiq-supported
// pipelines; managed-room is intentionally absent (accepted deviation).
var catalogModes = []string{"realtime", "transcription", "stt-tts"}

// Transport availability for talk.catalog. gateway-relay is the live server-
// owned transport; managed-room needs LiveKit infra metiq does not ship;
// webrtc / provider-websocket are browser-owned (talk.client.*) and only ready
// once a realtimevoice provider advertises browser-session support.
type transportDescriptor struct {
	ID    string `json:"id"`
	Owner string `json:"owner"`
	Ready bool   `json:"ready"`
	Note  string `json:"note,omitempty"`
}

// ProviderInfo is a catalog-shaped view of one realtime provider.
type ProviderInfo struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
}

// CatalogInput carries the runtime-resolved provider inventory for the catalog.
type CatalogInput struct {
	// SpeechProviders is tts.Manager.Providers() output (id/name/configured/voices).
	SpeechProviders []map[string]any
	// ActiveSpeech is the active tts provider id from talk/ops config.
	ActiveSpeech string
	// Transcription lists realtimestt providers (empty while unwired).
	Transcription []ProviderInfo
	// Realtime lists realtimevoice providers (empty while unwired).
	Realtime []ProviderInfo
	// BrowserRealtime reports whether any realtime provider supports browser-
	// owned sessions (talk.client.* create). False while unwired.
	BrowserRealtime bool
}

// BuildCatalog assembles the talk.catalog payload. The speech section reflects
// the live tts manager; the transcription and realtime sections honestly report
// ready:false with empty provider lists while the realtimestt/realtimevoice
// registries carry no providers.
func BuildCatalog(in CatalogInput) map[string]any {
	speech := in.SpeechProviders
	if speech == nil {
		speech = []map[string]any{}
	}
	speechReady := false
	for _, p := range speech {
		if configured, _ := p["configured"].(bool); configured {
			speechReady = true
			break
		}
	}

	transports := []transportDescriptor{
		{ID: "gateway-relay", Owner: "server", Ready: true},
		{ID: "managed-room", Owner: "server", Ready: false, Note: "requires managed-room/LiveKit infra (not available in metiq)"},
		{ID: "webrtc", Owner: "browser", Ready: in.BrowserRealtime, Note: browserNote(in.BrowserRealtime)},
		{ID: "provider-websocket", Owner: "browser", Ready: in.BrowserRealtime, Note: browserNote(in.BrowserRealtime)},
	}

	return map[string]any{
		"modes":      catalogModes,
		"transports": transports,
		"brains":     []map[string]any{{"id": "metiq", "name": "metiq agent runtime", "ready": true}},
		"speech": map[string]any{
			"ready":     speechReady,
			"providers": speech,
			"active":    in.ActiveSpeech,
		},
		"transcription": map[string]any{
			"ready":     len(in.Transcription) > 0,
			"providers": providerList(in.Transcription),
		},
		"realtime": map[string]any{
			"ready":     len(in.Realtime) > 0,
			"providers": providerList(in.Realtime),
		},
	}
}

func browserNote(ready bool) string {
	if ready {
		return ""
	}
	return "no realtimevoice provider advertises browser-session support"
}

func providerList(in []ProviderInfo) []ProviderInfo {
	if in == nil {
		return []ProviderInfo{}
	}
	return in
}
