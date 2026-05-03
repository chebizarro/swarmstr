package agent

import (
	"strings"
	"testing"
)

func TestNormalizeAckPrompt(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"simple lowercase", "ok", "ok"},
		{"uppercase", "OK", "ok"},
		{"mixed case", "Do It", "do it"},
		{"with punctuation", "ok!", "ok"},
		{"with multiple punctuation", "ok!!!", "ok"},
		{"with leading punctuation", "...ok", "ok"},
		{"with surrounding punctuation", "...ok!!!", "ok"},
		{"with extra spaces", "  ok  ", "ok"},
		{"multiple words with punctuation", "Go ahead!", "go ahead"},
		{"apostrophe in let's", "let's go", "let s go"},
		{"emoji removal", "ok 👍", "ok"},
		{"unicode normalization", "ＯＫ", "ok"}, // Full-width OK
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeAckPrompt(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeAckPrompt(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsAckExecutionPrompt(t *testing.T) {
	// Positive cases - should match
	positives := []string{
		"ok",
		"OK",
		"Ok",
		"okay",
		"OKAY",
		"do it",
		"Do it",
		"DO IT",
		"do it!",
		"go ahead",
		"Go ahead!",
		"please do",
		"sounds good",
		"ship it",
		"fix it",
		"make it so",
		"yes",
		"YES",
		"yep",
		"yeah",
		"sure",
		"proceed",
		"continue",
		"approved",
		"lgtm",
		"LGTM",
		"go",
		"go for it",
		"yes please",
		"k",
		"kk",
		"yup",
		"aye",
		"bet",
		"send it",
		"run it",
		"  ok  ",     // with whitespace
		"ok!",        // with punctuation
		"OK!!!",      // with multiple punctuation
		"...ok",      // with leading punctuation
		"let's go",   // with apostrophe
		"lets go",    // without apostrophe
		// Non-English
		"تمام",      // Arabic
		"やって",     // Japanese
		"進めて",     // Japanese
		"mach es",   // German
		"hazlo",     // Spanish
		"fais le",   // French
		"해줘",       // Korean
	}

	for _, text := range positives {
		t.Run("positive: "+text, func(t *testing.T) {
			if !IsAckExecutionPrompt(text) {
				t.Errorf("IsAckExecutionPrompt(%q) = false, want true", text)
			}
		})
	}

	// Negative cases - should NOT match
	negatives := []string{
		"",                                    // empty
		"ok but wait",                         // ACK with additional content
		"ok, but first let me explain",        // ACK with continuation
		"sounds good, but I have a question?", // ACK with question
		"can you do it?",                      // question
		"what do you think?",                  // question
		"please check the file",               // directive, not ACK
		"ok\nok",                              // multi-line
		"this is a much longer message that exceeds the 80 character limit for ACK prompts to prevent false positives", // too long
		"hello",           // not an ACK
		"thanks",          // not an ACK
		"good morning",    // not an ACK
		"I agree",         // agreement but not ACK pattern
		"let me think",    // not approval
		"maybe",           // not definitive
		"possibly",        // not definitive
		"not sure",        // not approval
	}

	for _, text := range negatives {
		t.Run("negative: "+text, func(t *testing.T) {
			if IsAckExecutionPrompt(text) {
				t.Errorf("IsAckExecutionPrompt(%q) = true, want false", text)
			}
		})
	}
}

func TestGetAckFastPathInstruction(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantEmpty   bool
	}{
		{"ACK returns instruction", "ok", false},
		{"ACK with punctuation returns instruction", "go ahead!", false},
		{"non-ACK returns empty", "hello", true},
		{"question returns empty", "ok?", true},
		{"long message returns empty", "ok but please also check the other file", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetAckFastPathInstruction(tt.input)
			if tt.wantEmpty && result != "" {
				t.Errorf("GetAckFastPathInstruction(%q) = %q, want empty", tt.input, result)
			}
			if !tt.wantEmpty && result == "" {
				t.Errorf("GetAckFastPathInstruction(%q) = empty, want instruction", tt.input)
			}
			if !tt.wantEmpty && result != AckFastPathInstruction {
				t.Errorf("GetAckFastPathInstruction(%q) = %q, want %q", tt.input, result, AckFastPathInstruction)
			}
		})
	}
}

func TestIsAckWithTrailingContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"short ACK - no trailing", "ok", false},
		{"short message - no trailing", "hello there", false},
		{"ACK with trailing content", "ok but also check the configuration file and make sure the tests pass before deploying", true},
		// "yes and please..." is only 73 chars, under the 80-char threshold for trailing detection
		{"yes with short trailing content", "yes and please also verify the database migrations are working correctly", false},
		// This one is over 80 chars so it should be detected
		{"go ahead with long trailing content", "go ahead with the implementation but be careful about the edge cases in the parsing logic and error handling", true},
		// "please" is in ACK set so this gets detected as ACK + trailing - expected behavior
		{"please with trailing content", "please check the configuration file and make sure the tests pass before deploying to production", true},
		// No ACK prefix at all
		{"long message no ACK prefix", "verify the configuration file and make sure the tests pass before deploying to production server", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAckWithTrailingContent(tt.input)
			if result != tt.expected {
				t.Errorf("IsAckWithTrailingContent(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestAckFastPathInstruction(t *testing.T) {
	// Ensure the instruction contains key guidance
	if !containsAllSubstrings(AckFastPathInstruction,
		"approval",
		"Do not recap",
		"tool action",
	) {
		t.Errorf("AckFastPathInstruction missing expected content: %q", AckFastPathInstruction)
	}
}

// containsAllSubstrings checks if s contains all of the given substrings
func containsAllSubstrings(s string, substrings ...string) bool {
	for _, sub := range substrings {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// TestAckPromptEdgeCases tests edge cases and boundary conditions
func TestAckPromptEdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		// Exactly at boundary (80 chars)
		{"exactly 80 chars ACK", "ok" + string(make([]byte, 78)), false}, // padded ok is not in set
		
		// Unicode edge cases
		{"full-width characters", "ＯＫ", true},
		{"mixed width", "ok", true},
		
		// Whitespace edge cases
		{"tabs", "\tok\t", true},
		{"only whitespace", "   ", false},
		{"newline only", "\n", false},
		
		// Punctuation edge cases
		{"only punctuation", "!!!", false},
		{"punctuation and ACK", "!ok!", true},
		{"dash prefix", "-ok", true},
		
		// Case sensitivity
		{"all caps", "LGTM", true},
		{"alternating case", "LgTm", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAckExecutionPrompt(tt.input)
			if result != tt.expected {
				t.Errorf("IsAckExecutionPrompt(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// Benchmark tests
func BenchmarkIsAckExecutionPrompt(b *testing.B) {
	inputs := []string{
		"ok",
		"go ahead",
		"this is a longer message that is not an ACK",
		"please check the file",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			IsAckExecutionPrompt(input)
		}
	}
}

func BenchmarkNormalizeAckPrompt(b *testing.B) {
	inputs := []string{
		"OK!",
		"Go ahead!!!",
		"let's go",
		"ＯＫ", // full-width
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, input := range inputs {
			normalizeAckPrompt(input)
		}
	}
}
