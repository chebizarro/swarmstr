package realtimestt

type TranscriptCallback func(text string, isFinal bool)

type SessionConfig struct {
	Language     string             `json:"language,omitempty"`
	Model        string             `json:"model,omitempty"`
	SampleRate   int                `json:"sample_rate,omitempty"`
	Encoding     string             `json:"encoding,omitempty"`
	Channels     int                `json:"channels,omitempty"`
	OnTranscript TranscriptCallback `json:"-"`
}

type Session interface {
	SendAudio(data []byte) error
	Close() error
	Done() <-chan struct{}
}
