package imagegen

// ImageGenerationRequest defines an image generation/edit/variation request.
type ImageGenerationRequest struct {
	Prompt         string       `json:"prompt"`
	NegativePrompt string       `json:"negative_prompt,omitempty"`
	Model          string       `json:"model,omitempty"`
	Size           string       `json:"size,omitempty"`
	Quality        string       `json:"quality,omitempty"`
	Format         string       `json:"format,omitempty"`
	N              int          `json:"n,omitempty"`
	SourceImage    *SourceImage `json:"source_image,omitempty"`
	Mask           string       `json:"mask,omitempty"`
	Mode           string       `json:"mode,omitempty"`
}

type SourceImage struct {
	URL    string `json:"url,omitempty"`
	Base64 string `json:"base64,omitempty"`
	Mime   string `json:"mime,omitempty"`
}

type ImageGenerationResult struct {
	Images   []GeneratedImage `json:"images"`
	Model    string           `json:"model,omitempty"`
	Provider string           `json:"provider,omitempty"`
	Usage    *UsageInfo       `json:"usage,omitempty"`
}

type GeneratedImage struct {
	URL       string `json:"url,omitempty"`
	Base64    string `json:"base64,omitempty"`
	Mime      string `json:"mime,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Seed      int64  `json:"seed,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
}

type UsageInfo struct {
	PromptTokens int     `json:"prompt_tokens,omitempty"`
	Cost         float64 `json:"cost,omitempty"`
}

type ProviderCapabilities struct {
	Generate  bool     `json:"generate"`
	Edit      bool     `json:"edit"`
	Variation bool     `json:"variation"`
	Inpaint   bool     `json:"inpaint"`
	Outpaint  bool     `json:"outpaint"`
	Sizes     []string `json:"sizes,omitempty"`
	Formats   []string `json:"formats,omitempty"`
	MaxN      int      `json:"max_n,omitempty"`
}
