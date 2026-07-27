package channels

import "testing"

func TestBuildPreviousEventTag(t *testing.T) {
	// Fewer than a full window: all referenced, 8-char prefixes.
	ids := []string{
		"aaaaaaaa1111111111111111111111111111111111111111111111111111aaaa",
		"bbbbbbbb2222222222222222222222222222222222222222222222222222bbbb",
	}
	tag := BuildPreviousEventTag(ids)
	if tag == nil || tag[0] != "previous" {
		t.Fatalf("expected a previous tag, got %v", tag)
	}
	if len(tag) != 3 || tag[1] != "aaaaaaaa" || tag[2] != "bbbbbbbb" {
		t.Errorf("previous tag = %v, want [previous aaaaaaaa bbbbbbbb]", tag)
	}
}

func TestBuildPreviousEventTag_Empty(t *testing.T) {
	if tag := BuildPreviousEventTag(nil); tag != nil {
		t.Errorf("no refs must yield nil, got %v", tag)
	}
	// Too-short ids are skipped -> nil.
	if tag := BuildPreviousEventTag([]string{"short"}); tag != nil {
		t.Errorf("short ids must be skipped, got %v", tag)
	}
}

func TestBuildPreviousEventTag_CapsAndDedupes(t *testing.T) {
	// 60 unique ids -> only the most recent 50 are referenced.
	ids := make([]string, 60)
	for i := range ids {
		ids[i] = string(rune('a'+i%26)) + "bcdefgh" + "0000000000000000000000000000000000000000000000000000000"
	}
	// Make each 8-char prefix unique enough by index.
	for i := range ids {
		p := []byte("00000000")
		p[0] = byte('a' + i%26)
		p[1] = byte('A' + i%26)
		p[2] = byte('0' + i%10)
		ids[i] = string(p) + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	}
	tag := BuildPreviousEventTag(ids)
	if tag == nil {
		t.Fatal("expected a tag")
	}
	// tag[0] == "previous"; refs capped at 50.
	if refs := len(tag) - 1; refs > NIP29MaxPreviousRefs {
		t.Errorf("refs=%d exceeds cap %d", refs, NIP29MaxPreviousRefs)
	}
}

func TestIsGeneratedFailureReplyPayload(t *testing.T) {
	failures := []string{
		"⚠️ Rate-limited by the provider, retry shortly",
		"⚠️ Anthropic returned a billing error",
		"The AI service is temporarily overloaded",
		"LLM request timed out after 60s",
		"Authentication is missing for the provider",
		"Context overflow: prompt too large for the model",
		"HTTP 503 from the upstream gateway",
		"Session history looks corrupted; starting fresh",
	}
	for _, f := range failures {
		if !IsGeneratedFailureReplyPayload(f) {
			t.Errorf("expected failure payload: %q", f)
		}
	}

	normal := []string{
		"",
		"Sure, I can help with that.",
		"The deployment finished successfully.",
		"Here is the status of your tasks.",
		"I rate limited myself to three retries in the config.", // not a leading failure marker
	}
	for _, n := range normal {
		if IsGeneratedFailureReplyPayload(n) {
			t.Errorf("normal text wrongly flagged as failure: %q", n)
		}
	}
}
