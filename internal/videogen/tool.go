package videogen

import (
	"context"
	"encoding/json"
	"fmt"

	"metiq/internal/agent"
)

func Tool(rt *Runtime) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if rt == nil {
			return "", fmt.Errorf("video generation runtime not configured")
		}
		req := VideoGenerationRequest{Prompt: agent.ArgString(args, "prompt"), Model: agent.ArgString(args, "model"), Duration: agent.ArgInt(args, "duration", 5), Resolution: agent.ArgString(args, "resolution"), AspectRatio: agent.ArgString(args, "aspect_ratio"), FPS: agent.ArgInt(args, "fps", 0), Mode: agent.ArgString(args, "mode")}
		sourceURL := agent.ArgString(args, "source_url")
		sourceB64 := agent.ArgString(args, "source_base64")
		if sourceURL != "" && sourceB64 != "" {
			return "", fmt.Errorf("source_url and source_base64 are mutually exclusive")
		}
		if sourceURL != "" {
			req.SourceAsset = &SourceAsset{URL: sourceURL, Mime: agent.ArgString(args, "source_mime"), Role: agent.ArgString(args, "source_role")}
		}
		if sourceB64 != "" {
			req.SourceAsset = &SourceAsset{Base64: sourceB64, Mime: agent.ArgString(args, "source_mime"), Role: agent.ArgString(args, "source_role")}
		}
		res, err := rt.Generate(ctx, agent.ArgString(args, "provider"), req)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(res)
		return string(out), nil
	}
}
