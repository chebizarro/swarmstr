package harness

import "metiq/internal/session/checkpoint"

func (s *Session) PersistCompactionToStore(store *checkpoint.Store, sessionKey string, e Entry) checkpoint.Checkpoint {
	var snap *checkpoint.Snapshot
	entries := s.storage.ReadAll()
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		ids = append(ids, entry.ID)
	}
	if len(ids) > 0 {
		snap = checkpoint.CaptureSnapshot(sessionKey, s.storage.Header().ID, ids)
	}
	return store.Persist(checkpoint.PersistParams{
		SessionKey: sessionKey, SessionID: s.storage.Header().ID, Reason: checkpoint.ReasonManual,
		Snapshot: snap, Summary: e.Summary, FirstKeptEntry: e.FirstKeptEntryID,
		DroppedEntries: e.DroppedEntries, KeptEntries: e.KeptEntries, TokensBefore: e.TokensBefore, TokensAfter: e.TokensAfter,
		PostEntryCount: len(ids), PostFirstEntry: first(ids), PostLastEntry: last(ids),
		FileOps: checkpoint.FileOperations{ReadFiles: e.FileOps.ReadFiles, WrittenFiles: e.FileOps.WrittenFiles, EditedFiles: e.FileOps.EditedFiles},
	})
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
func last(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}
