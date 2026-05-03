package realtimevoice

type AudioCallback func(audio []byte, format string)

type BridgeConfig struct {
	Model        string                         `json:"model,omitempty"`
	Voice        string                         `json:"voice,omitempty"`
	Language     string                         `json:"language,omitempty"`
	InputFormat  AudioFormat                    `json:"input_format"`
	OutputFormat AudioFormat                    `json:"output_format"`
	SystemPrompt string                         `json:"system_prompt,omitempty"`
	OnAudio      AudioCallback                  `json:"-"`
	OnTranscript func(text string, role string) `json:"-"`
}

type AudioFormat struct {
	Encoding   string `json:"encoding"`
	SampleRate int    `json:"sample_rate"`
	Channels   int    `json:"channels"`
}
type Bridge interface {
	SendAudio(data []byte) error
	SendText(text string) error
	Interrupt() error
	Close() error
	Done() <-chan struct{}
}
type VoiceInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Language    string `json:"language"`
	Gender      string `json:"gender,omitempty"`
	Description string `json:"description,omitempty"`
}
