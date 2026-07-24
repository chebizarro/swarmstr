package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	bedrockdocument "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	bedrocktypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

// bedrockConverseStreamer is the subset of the AWS client used by this runtime.
type bedrockConverseStreamer interface {
	ConverseStream(context.Context, *bedrockruntime.ConverseStreamInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// BedrockProvider implements Amazon Bedrock's native ConverseStream protocol.
type BedrockProvider struct {
	Model   string
	Region  string
	Profile string
	BaseURL string
	Client  bedrockConverseStreamer
}

func (p *BedrockProvider) Generate(ctx context.Context, turn Turn) (ProviderResult, error) {
	return p.StreamEvents(ctx, turn, nil)
}

func (p *BedrockProvider) Stream(ctx context.Context, turn Turn, onChunk func(string)) (ProviderResult, error) {
	return streamEventsAsLegacy(ctx, turn, onChunk, p)
}

func (p *BedrockProvider) StreamEvents(ctx context.Context, turn Turn, emit ProviderStreamEventSink) (ProviderResult, error) {
	return runProviderEventStream(emit, func(emit ProviderStreamEventSink) (ProviderResult, error) {
		input, err := p.buildInput(turn)
		if err != nil {
			return ProviderResult{}, err
		}
		client, err := p.client(ctx)
		if err != nil {
			return ProviderResult{}, err
		}
		out, err := client.ConverseStream(ctx, input)
		if err != nil {
			return ProviderResult{}, fmt.Errorf("bedrock converse stream: %w", err)
		}
		stream := out.GetStream()
		if stream == nil {
			return ProviderResult{}, fmt.Errorf("bedrock converse stream: missing event stream")
		}
		defer stream.Close()
		result, err := consumeBedrockStream(stream.Events(), emit)
		if err != nil {
			return ProviderResult{}, err
		}
		if err := stream.Err(); err != nil {
			return ProviderResult{}, fmt.Errorf("bedrock converse stream: %w", err)
		}
		return result, nil
	})
}

func (p *BedrockProvider) client(ctx context.Context) (bedrockConverseStreamer, error) {
	if p.Client != nil {
		return p.Client, nil
	}
	region := strings.TrimSpace(p.Region)
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_REGION"))
	}
	if region == "" {
		region = strings.TrimSpace(os.Getenv("AWS_DEFAULT_REGION"))
	}
	if region == "" {
		region = "us-east-1"
	}
	loadOpts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(region)}
	profile := strings.TrimSpace(p.Profile)
	if profile == "" {
		profile = strings.TrimSpace(os.Getenv("AWS_PROFILE"))
	}
	if profile != "" {
		loadOpts = append(loadOpts, awsconfig.WithSharedConfigProfile(profile))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, fmt.Errorf("load AWS configuration: %w", err)
	}
	return bedrockruntime.NewFromConfig(cfg, func(opts *bedrockruntime.Options) {
		if base := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/"); base != "" {
			opts.BaseEndpoint = aws.String(base)
		}
	}), nil
}

func (p *BedrockProvider) buildInput(turn Turn) (*bedrockruntime.ConverseStreamInput, error) {
	model := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p.Model), "bedrock/"))
	if model == "" || model == "bedrock" {
		return nil, fmt.Errorf("bedrock model id is required")
	}
	messages := buildLLMMessagesFromTurn(turn, "")
	var system []bedrocktypes.SystemContentBlock
	var wire []bedrocktypes.Message
	for _, msg := range messages {
		if msg.Role == "system" {
			if msg.Content != "" {
				system = append(system, &bedrocktypes.SystemContentBlockMemberText{Value: msg.Content})
			}
			continue
		}
		role := bedrocktypes.ConversationRoleUser
		if msg.Role == "assistant" {
			role = bedrocktypes.ConversationRoleAssistant
		}
		var blocks []bedrocktypes.ContentBlock
		if msg.Content != "" {
			blocks = append(blocks, &bedrocktypes.ContentBlockMemberText{Value: msg.Content})
		}
		for _, call := range msg.ToolCalls {
			args := call.Args
			if args == nil {
				args = map[string]any{}
			}
			input := bedrockdocument.NewLazyDocument(args)
			blocks = append(blocks, &bedrocktypes.ContentBlockMemberToolUse{Value: bedrocktypes.ToolUseBlock{ToolUseId: aws.String(call.ID), Name: aws.String(call.Name), Input: input}})
		}
		if msg.Role == "tool" {
			blocks = []bedrocktypes.ContentBlock{&bedrocktypes.ContentBlockMemberToolResult{Value: bedrocktypes.ToolResultBlock{ToolUseId: aws.String(msg.ToolCallID), Content: []bedrocktypes.ToolResultContentBlock{&bedrocktypes.ToolResultContentBlockMemberText{Value: msg.Content}}}}}
			role = bedrocktypes.ConversationRoleUser
		}
		if len(blocks) != 0 {
			// Converse requires alternating roles; consecutive tool results and
			// adjacent user messages belong in one user message.
			if len(wire) > 0 && wire[len(wire)-1].Role == role {
				wire[len(wire)-1].Content = append(wire[len(wire)-1].Content, blocks...)
			} else {
				wire = append(wire, bedrocktypes.Message{Role: role, Content: blocks})
			}
		}
	}
	input := &bedrockruntime.ConverseStreamInput{ModelId: aws.String(model), Messages: wire, System: system}
	maxTokens := turn.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	if turn.ThinkingBudget > 0 && maxTokens <= turn.ThinkingBudget {
		maxTokens = turn.ThinkingBudget + turn.ThinkingBudget/2
		if maxTokens < 16000 {
			maxTokens = 16000
		}
	}
	input.InferenceConfig = &bedrocktypes.InferenceConfiguration{MaxTokens: aws.Int32(int32(maxTokens))}
	if turn.ThinkingBudget > 0 {
		input.AdditionalModelRequestFields = bedrockdocument.NewLazyDocument(map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": turn.ThinkingBudget}})
	}
	if len(turn.Tools) > 0 {
		tools := make([]bedrocktypes.Tool, 0, len(turn.Tools))
		for _, def := range turn.Tools {
			schema := toolInputSchemaMap(def)
			tools = append(tools, &bedrocktypes.ToolMemberToolSpec{Value: bedrocktypes.ToolSpecification{Name: aws.String(def.Name), Description: aws.String(def.Description), InputSchema: &bedrocktypes.ToolInputSchemaMemberJson{Value: bedrockdocument.NewLazyDocument(schema)}}})
		}
		input.ToolConfig = &bedrocktypes.ToolConfiguration{Tools: tools}
	}
	return input, nil
}

type bedrockToolAccumulator struct {
	id, name  string
	arguments strings.Builder
}

func consumeBedrockStream(events <-chan bedrocktypes.ConverseStreamOutput, emit ProviderStreamEventSink) (ProviderResult, error) {
	var text strings.Builder
	var usage ProviderUsage
	tools := map[int]*bedrockToolAccumulator{}
	maxTool := -1
	for event := range events {
		switch value := event.(type) {
		case *bedrocktypes.ConverseStreamOutputMemberContentBlockStart:
			idx := int(aws.ToInt32(value.Value.ContentBlockIndex))
			if start, ok := value.Value.Start.(*bedrocktypes.ContentBlockStartMemberToolUse); ok {
				tools[idx] = &bedrockToolAccumulator{id: aws.ToString(start.Value.ToolUseId), name: aws.ToString(start.Value.Name)}
				if idx > maxTool {
					maxTool = idx
				}
				if emit != nil {
					emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: idx, ID: tools[idx].id, Name: tools[idx].name}})
				}
			}
		case *bedrocktypes.ConverseStreamOutputMemberContentBlockDelta:
			idx := int(aws.ToInt32(value.Value.ContentBlockIndex))
			switch delta := value.Value.Delta.(type) {
			case *bedrocktypes.ContentBlockDeltaMemberText:
				text.WriteString(delta.Value)
				if emit != nil && delta.Value != "" {
					emit(ProviderStreamEvent{Type: ProviderStreamTextDelta, TextDelta: delta.Value})
				}
			case *bedrocktypes.ContentBlockDeltaMemberReasoningContent:
				if reasoning, ok := delta.Value.(*bedrocktypes.ReasoningContentBlockDeltaMemberText); ok && reasoning.Value != "" && emit != nil {
					emit(ProviderStreamEvent{Type: ProviderStreamThinkingDelta, ThinkingDelta: reasoning.Value})
				}
			case *bedrocktypes.ContentBlockDeltaMemberToolUse:
				acc := tools[idx]
				if acc == nil {
					acc = &bedrockToolAccumulator{}
					tools[idx] = acc
				}
				fragment := aws.ToString(delta.Value.Input)
				acc.arguments.WriteString(fragment)
				if idx > maxTool {
					maxTool = idx
				}
				if emit != nil {
					emit(ProviderStreamEvent{Type: ProviderStreamToolCallDelta, ToolCallDelta: ProviderToolCallDelta{Index: idx, ID: acc.id, Name: acc.name, ArgumentsDelta: fragment}})
				}
			}
		case *bedrocktypes.ConverseStreamOutputMemberMetadata:
			if value.Value.Usage != nil {
				u := value.Value.Usage
				next := ProviderUsage{InputTokens: int64(aws.ToInt32(u.InputTokens)), OutputTokens: int64(aws.ToInt32(u.OutputTokens)), CacheReadTokens: int64(aws.ToInt32(u.CacheReadInputTokens)), CacheCreationTokens: int64(aws.ToInt32(u.CacheWriteInputTokens))}
				if hasProviderUsage(next) && !providerUsageEqual(next, usage) {
					usage = next
					if emit != nil {
						emit(ProviderStreamEvent{Type: ProviderStreamUsage, Usage: usage})
					}
				}
			}
		}
	}
	var calls []ToolCall
	for idx := 0; idx <= maxTool; idx++ {
		acc := tools[idx]
		if acc == nil || acc.name == "" {
			continue
		}
		args := map[string]any{}
		if raw := acc.arguments.String(); raw != "" {
			if err := json.Unmarshal([]byte(raw), &args); err != nil {
				return ProviderResult{}, fmt.Errorf("bedrock tool input: %w", err)
			}
		}
		calls = append(calls, ToolCall{ID: acc.id, Name: acc.name, Args: args})
	}
	if text.Len() == 0 && len(calls) == 0 {
		return ProviderResult{}, fmt.Errorf("bedrock converse stream: empty response")
	}
	return ProviderResult{Text: text.String(), ToolCalls: calls, Usage: usage}, nil
}

var _ EventStreamingProvider = (*BedrockProvider)(nil)
