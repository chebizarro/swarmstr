package imagegen

import (
	"context"
	"encoding/json"
	"fmt"

	"metiq/internal/agent"
)

func Tool(rt *Runtime) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if rt == nil {
			return "", fmt.Errorf("image generation runtime not configured")
		}
		req := ImageGenerationRequest{Prompt: agent.ArgString(args, "prompt"), NegativePrompt: agent.ArgString(args, "negative_prompt"), Model: agent.ArgString(args, "model"), Size: agent.ArgString(args, "size"), Quality: agent.ArgString(args, "quality"), Format: agent.ArgString(args, "format"), N: agent.ArgInt(args, "n", 1), Mode: agent.ArgString(args, "mode"), Mask: agent.ArgString(args, "mask")}
		sourceURL := agent.ArgString(args, "source_image_url")
		sourceB64 := agent.ArgString(args, "source_image_base64")
		if sourceURL != "" && sourceB64 != "" {
			return "", fmt.Errorf("source_image_url and source_image_base64 are mutually exclusive")
		}
		if sourceURL != "" {
			req.SourceImage = &SourceImage{URL: sourceURL, Mime: agent.ArgString(args, "source_image_mime")}
		}
		if sourceB64 != "" {
			req.SourceImage = &SourceImage{Base64: sourceB64, Mime: agent.ArgString(args, "source_image_mime")}
		}
		res, err := rt.Generate(ctx, agent.ArgString(args, "provider"), req)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(res)
		return string(out), nil
	}
}
