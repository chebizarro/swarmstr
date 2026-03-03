package state

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxTranscriptTextRunes = 8192
	maxTranscriptMetaBytes = 16 * 1024

	maxMemoryTextRunes   = 4096
	maxMemoryTopicRunes  = 128
	maxMemoryKeywords    = 32
	maxMemoryKeywordRune = 64
	maxMemoryMetaBytes   = 16 * 1024
)

func enforceTextLimit(field, value string, maxRunes int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s exceeds %d characters", field, maxRunes)
	}
	return nil
}

func enforceOptionalTextLimit(field, value string, maxRunes int) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	if utf8.RuneCountInString(strings.TrimSpace(value)) > maxRunes {
		return fmt.Errorf("%s exceeds %d characters", field, maxRunes)
	}
	return nil
}

func enforceMetaBytes(field string, meta map[string]any, maxBytes int) error {
	if len(meta) == 0 {
		return nil
	}
	raw, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("%s is not valid json", field)
	}
	if len(raw) > maxBytes {
		return fmt.Errorf("%s exceeds %d bytes", field, maxBytes)
	}
	return nil
}
