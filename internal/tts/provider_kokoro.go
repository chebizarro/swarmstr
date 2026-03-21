package tts

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// KokoroProvider implements Provider using the local Kokoro TTS binary.
// Kokoro (https://github.com/thewh1teagle/kokoro-onnx or similar) must be
// installed and available in PATH as "kokoro".
type KokoroProvider struct{}

func (p *KokoroProvider) ID() string   { return "kokoro" }
func (p *KokoroProvider) Name() string { return "Kokoro TTS (local)" }

func (p *KokoroProvider) Voices() []string {
	return []string{
		"af_sky", "af_bella", "af_sarah", "af_nicole",
		"am_adam", "am_michael",
		"bf_emma", "bf_isabella",
		"bm_george", "bm_lewis",
	}
}

func (p *KokoroProvider) Configured() bool {
	_, err := exec.LookPath("kokoro")
	return err == nil
}

func (p *KokoroProvider) Convert(ctx context.Context, text, voice string) ([]byte, string, error) {
	kokoroBin, err := exec.LookPath("kokoro")
	if err != nil {
		return nil, "", fmt.Errorf("kokoro binary not found in PATH: install from https://github.com/thewh1teagle/kokoro-onnx")
	}
	if voice == "" {
		voice = "af_sky"
	}

	// Write text to a temp input file so the CLI doesn't have shell-escaping issues.
	textFile, ferr := os.CreateTemp("", "metiq-kokoro-in-*.txt")
	if ferr != nil {
		return nil, "", fmt.Errorf("create text temp file: %w", ferr)
	}
	defer os.Remove(textFile.Name())
	if _, werr := textFile.WriteString(text); werr != nil {
		textFile.Close()
		return nil, "", fmt.Errorf("write text file: %w", werr)
	}
	textFile.Close()

	// Output to a temp wav file.
	outFile, ferr := os.CreateTemp("", "metiq-kokoro-out-*.wav")
	if ferr != nil {
		return nil, "", fmt.Errorf("create wav temp file: %w", ferr)
	}
	outPath := outFile.Name()
	outFile.Close()
	defer os.Remove(outPath)

	// Invoke: kokoro --voice=<voice> --text-file=<path> --output=<path>
	// Fallback invocation pattern; adapt if your kokoro binary uses different flags.
	cmd := exec.CommandContext(ctx, kokoroBin,
		"--voice="+voice,
		"--text-file="+textFile.Name(),
		"--output="+outPath,
	)
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if runErr := cmd.Run(); runErr != nil {
		return nil, "", fmt.Errorf("kokoro failed: %w: %s", runErr, errBuf.String())
	}

	data, readErr := os.ReadFile(outPath)
	if readErr != nil {
		return nil, "", fmt.Errorf("read kokoro output: %w", readErr)
	}
	return data, "wav", nil
}
