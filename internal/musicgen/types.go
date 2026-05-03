package musicgen

type MusicGenerationRequest struct {
	Prompt   string `json:"prompt"`
	Duration int    `json:"duration,omitempty"`
	Format   string `json:"format,omitempty"`
	Model    string `json:"model,omitempty"`
	Genre    string `json:"genre,omitempty"`
}

type MusicGenerationResult struct {
	Audio    GeneratedAudio `json:"audio"`
	Duration int            `json:"duration,omitempty"`
	Provider string         `json:"provider,omitempty"`
	Model    string         `json:"model,omitempty"`
}

type GeneratedAudio struct {
	URL       string `json:"url,omitempty"`
	Base64    string `json:"base64,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	Format    string `json:"format,omitempty"`
}
