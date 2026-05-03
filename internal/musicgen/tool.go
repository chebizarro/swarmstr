package musicgen

import (
	"context"
	"encoding/json"
	"fmt"

	"metiq/internal/agent"
)

type ToolOptions struct {
	Runtime     *Runtime
	MediaPrefix string
}

func Tool(opts ToolOptions) agent.ToolFunc {
	return func(ctx context.Context, args map[string]any) (string, error) {
		if opts.Runtime == nil {
			return "", fmt.Errorf("music generation runtime not configured")
		}
		req := MusicGenerationRequest{Prompt: agent.ArgString(args, "prompt"), Model: agent.ArgString(args, "model"), Duration: agent.ArgInt(args, "duration", 30), Format: agent.ArgString(args, "format"), Genre: agent.ArgString(args, "genre")}
		res, err := opts.Runtime.Generate(ctx, agent.ArgString(args, "provider"), req)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(res)
		if opts.MediaPrefix != "" && res.Audio.LocalPath != "" {
			return opts.MediaPrefix + res.Audio.LocalPath + "\n" + string(out), nil
		}
		return string(out), nil
	}
}
