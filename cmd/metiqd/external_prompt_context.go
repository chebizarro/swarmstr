package main

import (
	"fmt"
	"strings"

	"metiq/internal/agent"
	"metiq/internal/store/state"
)

func buildExternalSessionPromptContext(sessionID string) string {
	if !agent.IsExternalHookSession(sessionID) {
		return ""
	}
	block := agent.WrapExternalPromptData("Inbound request routed through an external hook session.", agent.ExternalPromptDataOptions{
		Source:         agent.ExternalContentSourceFromSessionID(sessionID),
		Label:          "External request metadata",
		Metadata:       map[string]string{"session_id": strings.TrimSpace(sessionID)},
		IncludeWarning: false,
	})
	if block == "" {
		return ""
	}
	return "## External Request Context\n" + block
}

func buildExternalChannelMetadataContext(cfg state.ConfigDoc, channelID, senderID, sessionID string) string {
	channelID = strings.TrimSpace(channelID)
	senderID = strings.TrimSpace(senderID)
	if channelID == "" && senderID == "" {
		return ""
	}

	source := agent.ExternalContentSourceChannelMetadata
	channelKind := ""
	if cfg.NostrChannels != nil {
		if chanCfg, ok := cfg.NostrChannels[channelID]; ok {
			channelKind = strings.TrimSpace(chanCfg.Kind)
			if strings.EqualFold(channelKind, "email") {
				source = agent.ExternalContentSourceEmail
			}
		}
	}

	lines := []string{
		fmt.Sprintf("channel_id=%s", channelID),
		fmt.Sprintf("sender_id=%s", senderID),
	}
	metadata := map[string]string{
		"channel_id": channelID,
		"sender_id":  senderID,
	}
	if channelKind != "" {
		lines = append(lines, fmt.Sprintf("channel_kind=%s", channelKind))
		metadata["channel_kind"] = channelKind
	}
	if threadID := threadIDFromSessionID(sessionID); threadID != "" {
		lines = append(lines, fmt.Sprintf("thread_id=%s", threadID))
		metadata["thread_id"] = threadID
	}

	block := agent.WrapExternalPromptData(strings.Join(lines, "\n"), agent.ExternalPromptDataOptions{
		Source:         source,
		Label:          "External channel metadata",
		Sender:         senderID,
		Metadata:       metadata,
		IncludeWarning: false,
	})
	if block == "" {
		return ""
	}
	return "## External Channel Metadata\n" + block
}

func threadIDFromSessionID(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if idx := strings.Index(sessionID, ":thread:"); idx >= 0 {
		return strings.TrimSpace(sessionID[idx+len(":thread:"):])
	}
	return ""
}
