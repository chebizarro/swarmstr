package agent

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestConsumeBedrockStreamNormalizesStructuredEvents(t *testing.T) {
	events := make(chan bedrocktypes.ConverseStreamOutput, 8)
	events <- &bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(0), Delta: &bedrocktypes.ContentBlockDeltaMemberReasoningContent{Value: &bedrocktypes.ReasoningContentBlockDeltaMemberText{Value: "consider"}}}}
	events <- &bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(1), Delta: &bedrocktypes.ContentBlockDeltaMemberText{Value: "hello"}}}
	events <- &bedrocktypes.ConverseStreamOutputMemberContentBlockStart{Value: bedrocktypes.ContentBlockStartEvent{ContentBlockIndex: aws.Int32(2), Start: &bedrocktypes.ContentBlockStartMemberToolUse{Value: bedrocktypes.ToolUseBlockStart{ToolUseId: aws.String("call-1"), Name: aws.String("lookup")}}}}
	events <- &bedrocktypes.ConverseStreamOutputMemberContentBlockDelta{Value: bedrocktypes.ContentBlockDeltaEvent{ContentBlockIndex: aws.Int32(2), Delta: &bedrocktypes.ContentBlockDeltaMemberToolUse{Value: bedrocktypes.ToolUseBlockDelta{Input: aws.String(`{"q":"x"}`)}}}}
	events <- &bedrocktypes.ConverseStreamOutputMemberMetadata{Value: bedrocktypes.ConverseStreamMetadataEvent{Usage: &bedrocktypes.TokenUsage{InputTokens: aws.Int32(10), OutputTokens: aws.Int32(4), TotalTokens: aws.Int32(14), CacheReadInputTokens: aws.Int32(3), CacheWriteInputTokens: aws.Int32(2)}, Metrics: &bedrocktypes.ConverseStreamMetrics{LatencyMs: aws.Int64(1)}}}
	close(events)

	var normalized []ProviderStreamEvent
	result, err := consumeBedrockStream(events, func(event ProviderStreamEvent) { normalized = append(normalized, event) })
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "hello" || len(result.ToolCalls) != 1 || result.ToolCalls[0].Name != "lookup" || result.ToolCalls[0].Args["q"] != "x" {
		t.Fatalf("result = %#v", result)
	}
	if result.Usage.InputTokens != 10 || result.Usage.OutputTokens != 4 || result.Usage.CacheReadTokens != 3 || result.Usage.CacheCreationTokens != 2 {
		t.Fatalf("usage = %#v", result.Usage)
	}
	want := []ProviderStreamEventType{ProviderStreamThinkingDelta, ProviderStreamTextDelta, ProviderStreamToolCallDelta, ProviderStreamToolCallDelta, ProviderStreamUsage}
	if len(normalized) != len(want) {
		t.Fatalf("events = %#v", normalized)
	}
	for i := range want {
		if normalized[i].Type != want[i] {
			t.Fatalf("event %d type = %s, want %s", i, normalized[i].Type, want[i])
		}
	}
}
