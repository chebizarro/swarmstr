package catalog

import "testing"

func TestDefaultRegistryIndexesProvidersAndConfig(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test")
	r := DefaultRegistry(map[string]ProviderConfig{"ollama": {Model: "llama3.2"}})
	if _, ok := r.Get("llama3.2"); !ok {
		t.Fatal("configured provider model missing")
	}
	openai, ok := r.Get("gpt-4o")
	if !ok || !openai.Configured {
		t.Fatalf("expected configured OpenAI model, got %+v", openai)
	}
	if len(r.ByProvider("ollama")) != 1 {
		t.Fatalf("provider index missing ollama: %+v", r.Providers())
	}
}

func TestSearchCapabilitiesAndFilters(t *testing.T) {
	r := NewRegistry()
	r.Register(Model{ID: "m1", Provider: "p", ContextWindow: 100, Capabilities: []string{"tools"}, Modalities: []string{"text"}}, Model{ID: "m2", Provider: "p", ContextWindow: 10, Capabilities: []string{"chat"}})
	got := r.Search(Filter{Provider: "p", Capability: "tools", MinContext: 50})
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("unexpected search result: %+v", got)
	}
	if len(r.WithCapability("chat")) != 1 {
		t.Fatalf("capability query failed")
	}
}

func TestToMapsPreservesAPIShape(t *testing.T) {
	maps := ToMaps([]Model{{ID: "m", Name: "Model", Provider: "p", ContextWindow: 42, Reasoning: true, Configured: true}})
	if len(maps) != 1 || maps[0]["id"] != "m" || maps[0]["context_window"] != 42 || maps[0]["reasoning"] != true {
		t.Fatalf("bad map shape: %+v", maps)
	}
}
