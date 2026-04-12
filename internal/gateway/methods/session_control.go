package methods

import (
	"context"
	"fmt"
	"time"

	exportpkg "metiq/internal/export"
	"metiq/internal/store/state"
)

func GetSessionWithTranscript(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, sessionID string, limit int) (SessionGetResponse, error) {
	if docsRepo == nil {
		return SessionGetResponse{}, fmt.Errorf("docs repository is nil")
	}
	session, err := docsRepo.GetSession(ctx, sessionID)
	if err != nil {
		return SessionGetResponse{}, err
	}
	transcript, err := transcriptRepo.ListSession(ctx, sessionID, limit)
	if err != nil {
		return SessionGetResponse{}, err
	}
	return SessionGetResponse{Session: session, Transcript: transcript}, nil
}

func GetChatHistory(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, sessionID string, limit int) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("docs repository is nil")
	}
	if _, err := docsRepo.GetSession(ctx, sessionID); err != nil {
		return nil, err
	}
	transcript, err := transcriptRepo.ListSession(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	return ApplyCompatResponseAliases(map[string]any{"session_id": sessionID, "entries": transcript}), nil
}

func PreviewSession(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, sessionID string, limit int) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("docs repository is nil")
	}
	session, err := docsRepo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	transcript, err := transcriptRepo.ListSession(ctx, sessionID, limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{"session": session, "preview": transcript}, nil
}

func ExportSessionHTML(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, sessionID, fromPubKey string) (SessionsExportResponse, error) {
	if docsRepo == nil {
		return SessionsExportResponse{}, fmt.Errorf("docs repository is nil")
	}
	entries, err := transcriptRepo.ListSessionAll(ctx, sessionID)
	if err != nil {
		return SessionsExportResponse{}, fmt.Errorf("sessions.export: load transcript: %w", err)
	}
	msgs := make([]exportpkg.Message, 0, len(entries))
	for _, e := range entries {
		if e.Role == "deleted" || e.Role == "" {
			continue
		}
		msgs = append(msgs, exportpkg.Message{Role: e.Role, Content: e.Text, Timestamp: e.Unix, ID: e.EntryID})
	}
	agentName := ""
	if agDoc, err2 := docsRepo.GetAgent(ctx, "main"); err2 == nil {
		agentName = agDoc.Name
	}
	htmlOut, err := exportpkg.SessionToHTML(exportpkg.SessionHTMLOptions{
		SessionID:  sessionID,
		AgentID:    "main",
		AgentName:  agentName,
		PubKey:     fromPubKey,
		Messages:   msgs,
		ExportedAt: time.Now(),
	})
	if err != nil {
		return SessionsExportResponse{}, fmt.Errorf("sessions.export: render: %w", err)
	}
	return SessionsExportResponse{HTML: htmlOut, Format: "html"}, nil
}
