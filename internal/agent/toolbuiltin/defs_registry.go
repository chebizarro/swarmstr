package toolbuiltin

import "metiq/internal/agent"

var ImageGenerateDef = agent.ToolDefinition{
	Name:        "image_generate",
	Description: "Generate, edit, or create variations of images using configured AI image generation providers. Returns JSON with local output paths.",
	Parameters: agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{
		"prompt":              {Type: "string", Description: "Detailed prompt describing the desired image."},
		"provider":            {Type: "string", Description: "Optional provider ID (e.g. openai, midjourney, stable-diffusion, or plugin provider)."},
		"model":               {Type: "string", Description: "Optional provider-specific model."},
		"size":                {Type: "string", Description: "Image size such as 1024x1024."},
		"quality":             {Type: "string", Description: "Quality level.", Enum: []string{"low", "medium", "high", "standard", "hd", "auto"}},
		"format":              {Type: "string", Description: "Output format.", Enum: []string{"png", "jpeg", "jpg", "webp"}},
		"negative_prompt":     {Type: "string", Description: "What to avoid in the generated image."},
		"n":                   {Type: "integer", Description: "Number of images to generate."},
		"mode":                {Type: "string", Description: "Generation mode.", Enum: []string{"generate", "edit", "variation"}},
		"source_image_url":    {Type: "string", Description: "Source image URL for edit/variation mode."},
		"source_image_base64": {Type: "string", Description: "Source image base64 for edit/variation mode."},
		"source_image_mime":   {Type: "string", Description: "MIME type for source image."},
		"mask":                {Type: "string", Description: "Base64 PNG mask for edit/inpainting mode."},
	}, Required: []string{"prompt"}},
	ParamAliases: map[string]string{"negativePrompt": "negative_prompt", "count": "n", "sourceImageUrl": "source_image_url", "sourceImageBase64": "source_image_base64", "sourceImageMime": "source_image_mime"},
}

var VideoGenerateDef = agent.ToolDefinition{
	Name:        "video_generate",
	Description: "Generate videos from text or source frames using configured AI video generation providers. Handles async job polling and returns JSON with local output paths.",
	Parameters: agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{
		"prompt":        {Type: "string", Description: "Prompt describing the desired video."},
		"provider":      {Type: "string", Description: "Optional provider ID."},
		"model":         {Type: "string", Description: "Optional provider-specific model."},
		"duration":      {Type: "integer", Description: "Desired duration in seconds."},
		"resolution":    {Type: "string", Description: "Resolution such as 480P, 720P, 1080P."},
		"aspect_ratio":  {Type: "string", Description: "Aspect ratio such as 16:9, 9:16, 1:1."},
		"fps":           {Type: "integer", Description: "Frames per second."},
		"mode":          {Type: "string", Description: "Generation mode.", Enum: []string{"generate", "image_to_video", "video_to_video"}},
		"source_url":    {Type: "string", Description: "Source image/video URL for conditioned generation."},
		"source_base64": {Type: "string", Description: "Source image/video base64 for conditioned generation."},
		"source_mime":   {Type: "string", Description: "MIME type for source asset."},
		"source_role":   {Type: "string", Description: "Source role, e.g. first_frame, last_frame, reference."},
	}, Required: []string{"prompt"}},
	ParamAliases: map[string]string{"aspectRatio": "aspect_ratio", "sourceUrl": "source_url", "sourceBase64": "source_base64", "sourceMime": "source_mime", "sourceRole": "source_role"},
}

var MusicGenerateDef = agent.ToolDefinition{
	Name:        "music_generate",
	Description: "Generate music or audio from a text prompt using configured AI music generation providers. Returns JSON and a MEDIA path when audio is saved.",
	Parameters: agent.ToolParameters{Type: "object", Properties: map[string]agent.ToolParamProp{
		"prompt":   {Type: "string", Description: "Prompt describing the desired music."},
		"provider": {Type: "string", Description: "Optional provider ID."},
		"model":    {Type: "string", Description: "Optional provider-specific model."},
		"duration": {Type: "integer", Description: "Desired duration in seconds."},
		"format":   {Type: "string", Description: "Audio output format.", Enum: []string{"mp3", "wav", "ogg", "flac"}},
		"genre":    {Type: "string", Description: "Optional genre/style hint."},
	}, Required: []string{"prompt"}},
	ParamAliases: map[string]string{"length": "duration"},
}
