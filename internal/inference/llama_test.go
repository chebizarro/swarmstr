package inference

import (
	"encoding/json"
	"testing"

	"metiq/internal/agent"
)

// ─── SlotID Tests ─────────────────────────────────────────────────────────────

func TestSlotID_ReturnsValidRange(t *testing.T) {
	testCases := []struct {
		name    string
		agentID string
	}{
		{"pubkey1", "npub1abc123"},
		{"pubkey2", "npub1def456"},
		{"pubkey3", "npub1ghi789"},
		{"short", "a"},
		{"long", "npub1verylongpublickeywithlotsofcharacterstotest"},
		{"special", "test@example.com"},
		{"uuid", "550e8400-e29b-41d4-a716-446655440000"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			slot := SlotID(tc.agentID)
			if slot < 0 || slot >= SlotCount {
				t.Errorf("SlotID(%q) = %d, want value in range [0, %d]", tc.agentID, slot, SlotCount-1)
			}
		})
	}
}

func TestSlotID_Deterministic(t *testing.T) {
	agentID := "npub1test123"
	
	// Call SlotID multiple times with the same input
	results := make([]int, 100)
	for i := 0; i < 100; i++ {
		results[i] = SlotID(agentID)
	}
	
	// All results should be identical
	first := results[0]
	for i, slot := range results {
		if slot != first {
			t.Errorf("SlotID is not deterministic: call %d returned %d, expected %d", i, slot, first)
		}
	}
}

func TestSlotID_DifferentInputsProduceDifferentSlots(t *testing.T) {
	// Test that different agent IDs produce different slots (with high probability)
	slots := make(map[int]string)
	agents := []string{
		"npub1aaa",
		"npub1bbb",
		"npub1ccc",
		"npub1ddd",
		"npub1eee",
		"npub1fff",
	}
	
	for _, agentID := range agents {
		slot := SlotID(agentID)
		if existing, ok := slots[slot]; ok {
			t.Logf("Collision: %q and %q both map to slot %d (this is expected occasionally)", agentID, existing, slot)
		}
		slots[slot] = agentID
	}
	
	// We should have at least 2 different slots from 6 inputs
	if len(slots) < 2 {
		t.Errorf("Expected at least 2 different slots from %d inputs, got %d", len(agents), len(slots))
	}
}

func TestSlotID_EmptyAgentID(t *testing.T) {
	slot := SlotID("")
	if slot != DynamicSlot {
		t.Errorf("SlotID(\"\") = %d, want %d (DynamicSlot)", slot, DynamicSlot)
	}
}

// ─── BuildLlamaRequest Tests ──────────────────────────────────────────────────

func TestBuildLlamaRequest_SetsAllFields(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	agentID := "npub1test"
	model := "llama-3-8b"
	maxTokens := 2048
	
	req := BuildLlamaRequest(messages, agentID, model, maxTokens)
	
	if req.Model != model {
		t.Errorf("Model = %q, want %q", req.Model, model)
	}
	if len(req.Messages) != len(messages) {
		t.Errorf("Messages length = %d, want %d", len(req.Messages), len(messages))
	}
	if req.MaxTokens != maxTokens {
		t.Errorf("MaxTokens = %d, want %d", req.MaxTokens, maxTokens)
	}
	if !req.Stream {
		t.Error("Stream = false, want true")
	}
	if !req.CachePrompt {
		t.Error("CachePrompt = false, want true")
	}
}

func TestBuildLlamaRequest_SetsValidSlot(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	agentID := "npub1test"
	
	req := BuildLlamaRequest(messages, agentID, "llama-3-8b", 2048)
	
	if req.IDSlot < 0 || req.IDSlot >= SlotCount {
		t.Errorf("IDSlot = %d, want value in range [0, %d]", req.IDSlot, SlotCount-1)
	}
}

func TestBuildLlamaRequest_EmptyAgentIDProducesDynamicSlot(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	
	req := BuildLlamaRequest(messages, "", "llama-3-8b", 2048)
	
	if req.IDSlot != DynamicSlot {
		t.Errorf("IDSlot = %d, want %d (DynamicSlot) for empty agentID", req.IDSlot, DynamicSlot)
	}
}

func TestBuildLlamaRequest_CachePromptAlwaysTrue(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	
	// Test with agent ID
	req1 := BuildLlamaRequest(messages, "npub1test", "llama-3-8b", 2048)
	if !req1.CachePrompt {
		t.Error("CachePrompt = false, want true (with agent ID)")
	}
	
	// Test without agent ID
	req2 := BuildLlamaRequest(messages, "", "llama-3-8b", 2048)
	if !req2.CachePrompt {
		t.Error("CachePrompt = false, want true (without agent ID)")
	}
}

// ─── BuildAnthropicRequest Tests ──────────────────────────────────────────────

func TestBuildAnthropicRequest_SetsAllFields(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	model := "claude-sonnet-4"
	maxTokens := 4096
	
	req := BuildAnthropicRequest(messages, model, maxTokens)
	
	if req.Model != model {
		t.Errorf("Model = %q, want %q", req.Model, model)
	}
	if len(req.Messages) != len(messages) {
		t.Errorf("Messages length = %d, want %d", len(req.Messages), len(messages))
	}
	if req.MaxTokens != maxTokens {
		t.Errorf("MaxTokens = %d, want %d", req.MaxTokens, maxTokens)
	}
	if !req.Stream {
		t.Error("Stream = false, want true")
	}
}

// ─── JSON Serialization Tests ─────────────────────────────────────────────────

func TestLlamaRequest_JSONContainsSlotFields(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	req := BuildLlamaRequest(messages, "npub1test", "llama-3-8b", 2048)
	
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal LlamaRequest: %v", err)
	}
	
	jsonStr := string(data)
	
	// Check that id_slot and cache_prompt are present
	if !contains(jsonStr, "id_slot") {
		t.Error("JSON missing 'id_slot' field")
	}
	if !contains(jsonStr, "cache_prompt") {
		t.Error("JSON missing 'cache_prompt' field")
	}
}

func TestAnthropicRequest_JSONExcludesSlotFields(t *testing.T) {
	messages := []agent.LLMMessage{
		{Role: "user", Content: "Hello"},
	}
	req := BuildAnthropicRequest(messages, "claude-sonnet-4", 4096)
	
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Failed to marshal AnthropicRequest: %v", err)
	}
	
	jsonStr := string(data)
	
	// Check that id_slot and cache_prompt are NOT present
	if contains(jsonStr, "id_slot") {
		t.Error("JSON contains 'id_slot' field (should not be present)")
	}
	if contains(jsonStr, "cache_prompt") {
		t.Error("JSON contains 'cache_prompt' field (should not be present)")
	}
}

// ─── SlotCount Configuration Tests ────────────────────────────────────────────

func TestSlotCount_IsConfigurable(t *testing.T) {
	// Test that SlotCount can be changed without recompiling
	originalSlotCount := SlotCount
	defer func() { SlotCount = originalSlotCount }()
	
	// Change SlotCount
	SlotCount = 12
	
	// Verify SlotID respects the new count
	agentID := "npub1test"
	slot := SlotID(agentID)
	
	if slot < 0 || slot >= SlotCount {
		t.Errorf("SlotID(%q) = %d, want value in range [0, %d] after changing SlotCount to %d", 
			agentID, slot, SlotCount-1, SlotCount)
	}
}

func TestSlotCount_AffectsDistribution(t *testing.T) {
	originalSlotCount := SlotCount
	defer func() { SlotCount = originalSlotCount }()
	
	// Test with different SlotCount values
	for _, count := range []int{3, 6, 12} {
		SlotCount = count
		
		// Generate slots for multiple agents
		slots := make(map[int]int)
		for i := 0; i < 100; i++ {
			agentID := "npub1agent" + string(rune('a'+i))
			slot := SlotID(agentID)
			
			if slot < 0 || slot >= SlotCount {
				t.Errorf("SlotID produced slot %d outside range [0, %d) with SlotCount=%d", 
					slot, SlotCount, SlotCount)
			}
			slots[slot]++
		}
		
		t.Logf("SlotCount=%d: distribution across %d slots", count, len(slots))
	}
}

// ─── Helper Functions ─────────────────────────────────────────────────────────

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
