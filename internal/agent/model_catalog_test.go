package agent

import "testing"

func TestCatalogAliasResolution(t *testing.T) {
	row, ok := resolveCatalogModelRef("moonshot/kimi-k2")
	if !ok || row.ProviderID != "moonshot" || row.ID != "kimi-k2-0711-preview" {
		t.Fatalf("bad row: %+v ok=%v", row, ok)
	}
}

func TestDefaultRegistryNewProviders(t *testing.T) {
	reg := NewProviderRegistry()
	for _, d := range builtinProviderDescriptors() {
		if err := reg.Register(d); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"mistral", "moonshot", "openai-responses", "azure-responses", "vertex", "minimax", "minimax-cn"} {
		if _, ok := reg.Descriptor(id); !ok {
			t.Fatalf("missing descriptor %s", id)
		}
	}
	if desc, ok := reg.Match("responses/gpt-4.1"); !ok || desc.ID != "openai-responses" {
		t.Fatalf("bad responses match: %+v ok=%v", desc, ok)
	}
}
