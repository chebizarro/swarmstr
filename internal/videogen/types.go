package videogen

type VideoGenerationRequest struct {
	Prompt      string       `json:"prompt"`
	Model       string       `json:"model,omitempty"`
	Duration    int          `json:"duration,omitempty"`
	Resolution  string       `json:"resolution,omitempty"`
	AspectRatio string       `json:"aspect_ratio,omitempty"`
	FPS         int          `json:"fps,omitempty"`
	Mode        string       `json:"mode,omitempty"`
	SourceAsset *SourceAsset `json:"source_asset,omitempty"`
}

type SourceAsset struct {
	URL    string `json:"url,omitempty"`
	Base64 string `json:"base64,omitempty"`
	Mime   string `json:"mime,omitempty"`
	Role   string `json:"role,omitempty"`
}

type VideoGenerationResult struct {
	Videos   []GeneratedVideo `json:"videos,omitempty"`
	Status   string           `json:"status"`
	JobID    string           `json:"job_id,omitempty"`
	Provider string           `json:"provider,omitempty"`
	Model    string           `json:"model,omitempty"`
	Error    string           `json:"error,omitempty"`
}

type GeneratedVideo struct {
	URL       string `json:"url,omitempty"`
	Base64    string `json:"base64,omitempty"`
	LocalPath string `json:"local_path,omitempty"`
	Duration  int    `json:"duration,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Format    string `json:"format,omitempty"`
}

type ProviderCapabilities struct {
	Generate      bool     `json:"generate"`
	ImageToVideo  bool     `json:"image_to_video"`
	VideoToVideo  bool     `json:"video_to_video"`
	Resolutions   []string `json:"resolutions,omitempty"`
	MaxDuration   int      `json:"max_duration,omitempty"`
	AspectRatios  []string `json:"aspect_ratios,omitempty"`
	SupportsAsync bool     `json:"supports_async"`
}
