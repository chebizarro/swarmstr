package methods

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	result, err := PreviewSessions(ctx, docsRepo, transcriptRepo, SessionsPreviewRequest{SessionID: sessionID, Limit: limit})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func PreviewSessions(ctx context.Context, docsRepo *state.DocsRepository, transcriptRepo *state.TranscriptRepository, req SessionsPreviewRequest) (map[string]any, error) {
	if docsRepo == nil {
		return nil, fmt.Errorf("docs repository is nil")
	}
	if transcriptRepo == nil {
		return nil, fmt.Errorf("transcript repository is nil")
	}
	if len(req.Keys) > 0 {
		previews := make([]map[string]any, 0, len(req.Keys))
		for _, key := range req.Keys {
			session, err := docsRepo.GetSession(ctx, key)
			if err != nil {
				if errors.Is(err, state.ErrNotFound) {
					previews = append(previews, map[string]any{"key": key, "status": "missing", "items": []map[string]any{}})
					continue
				}
				log.Printf("sessions.preview: failed to get session %q: %v", key, err)
				previews = append(previews, map[string]any{"key": key, "status": "error", "items": []map[string]any{}})
				continue
			}
			transcript, err := transcriptRepo.ListSession(ctx, session.SessionID, req.Limit)
			if err != nil {
				log.Printf("sessions.preview: failed to list transcript for session %q: %v", key, err)
				previews = append(previews, map[string]any{"key": key, "status": "error", "items": []map[string]any{}})
				continue
			}
			previews = append(previews, map[string]any{"key": key, "status": previewStatus(transcript), "items": previewItems(transcript)})
		}
		return map[string]any{"ts": time.Now().UnixMilli(), "previews": previews}, nil
	}

	session, err := docsRepo.GetSession(ctx, req.SessionID)
	if err != nil {
		return nil, err
	}
	transcript, err := transcriptRepo.ListSession(ctx, session.SessionID, req.Limit)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"session": session,
		"preview": transcript,
		"ts":      time.Now().UnixMilli(),
		"previews": []map[string]any{{
			"key":    req.SessionID,
			"status": previewStatus(transcript),
			"items":  previewItems(transcript),
		}},
	}, nil
}

func previewItems(transcript []state.TranscriptEntryDoc) []map[string]any {
	items := make([]map[string]any, 0, len(transcript))
	for _, entry := range transcript {
		items = append(items, map[string]any{"role": entry.Role, "text": entry.Text})
	}
	return items
}

func previewStatus(transcript []state.TranscriptEntryDoc) string {
	if len(transcript) == 0 {
		return "empty"
	}
	return "ok"
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
