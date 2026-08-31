package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"metiq/internal/store/state"
)

type sessionMaintenanceVictim struct {
	Key       string
	SessionID string
	Reason    string
	UpdatedAt time.Time
	Bytes     int64
}

type sessionMaintenanceReport struct {
	Mode            string
	Total           int
	Protected       int
	Candidates      []sessionMaintenanceVictim
	Deleted         []string
	Archived        []string
	Skipped         []string
	DiskBytesBefore int64
	DiskBytesAfter  int64
}

type sessionMaintenanceService struct {
	docs        *state.DocsRepository
	transcripts *state.TranscriptRepository
	sessions    *state.SessionStore
	getConfig   func() state.SessionConfig
	protected   func() map[string]struct{}
	now         func() time.Time
	mu          sync.Mutex
}

func newSessionMaintenanceService(docs *state.DocsRepository, transcripts *state.TranscriptRepository, sessions *state.SessionStore, getConfig func() state.SessionConfig) *sessionMaintenanceService {
	return &sessionMaintenanceService{docs: docs, transcripts: transcripts, sessions: sessions, getConfig: getConfig, now: time.Now}
}

func (s *sessionMaintenanceService) Run(ctx context.Context) {
	if s == nil || s.sessions == nil || s.transcripts == nil || s.docs == nil {
		return
	}
	_, _ = s.RunOnce(ctx, true)
	for {
		cfg := state.ResolveSessionMaintenanceConfig(s.getConfig())
		if cfg.IntervalSeconds <= 0 {
			return
		}
		timer := time.NewTimer(time.Duration(cfg.IntervalSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			if report, err := s.RunOnce(ctx, false); err != nil {
				log.Printf("session maintenance: %v", err)
			} else if len(report.Candidates) > 0 {
				log.Printf("session maintenance: mode=%s total=%d protected=%d candidates=%d deleted=%d disk_before=%d disk_after=%d", report.Mode, report.Total, report.Protected, len(report.Candidates), len(report.Deleted), report.DiskBytesBefore, report.DiskBytesAfter)
			}
		}
	}
}

func (s *sessionMaintenanceService) RunOnce(ctx context.Context, force bool) (sessionMaintenanceReport, error) {
	if !s.mu.TryLock() {
		return sessionMaintenanceReport{}, fmt.Errorf("maintenance pass already running")
	}
	defer s.mu.Unlock()
	cfg := state.ResolveSessionMaintenanceConfig(s.getConfig())
	now := s.now()
	entries := s.sessions.List()
	report := sessionMaintenanceReport{Mode: cfg.Mode, Total: len(entries)}
	report.DiskBytesBefore = sessionMaintenanceDiskUsage(s.sessions.Path(), defaultSessionArtifactsRoot())
	protected := s.protectedSet(entries, cfg, now)
	report.Protected = len(protected)

	var eligible []sessionMaintenanceVictim
	for key, entry := range entries {
		if _, ok := protected[key]; ok {
			continue
		}
		updated := entry.UpdatedAt
		if updated.IsZero() {
			updated = entry.CreatedAt
		}
		age := now.Sub(updated)
		reason := ""
		if isShortModelRunSession(key, entry) && cfg.ModelRunPruneAfterSeconds > 0 && age >= time.Duration(cfg.ModelRunPruneAfterSeconds)*time.Second {
			reason = "model-run-retention"
		} else if cfg.PruneAfterSeconds > 0 && age >= time.Duration(cfg.PruneAfterSeconds)*time.Second {
			reason = "age"
		}
		eligible = append(eligible, sessionMaintenanceVictim{Key: key, SessionID: firstSessionID(key, entry.SessionID), Reason: reason, UpdatedAt: updated, Bytes: sessionEntryLocalBytes(entry)})
	}
	// Session-local file references are often absent even though transcript and
	// journal storage contributes to the global budget. Attribute an average
	// share so disk-pressure selection makes bounded progress instead of
	// selecting every zero-byte candidate.
	averageBytes := int64(0)
	if len(entries) > 0 {
		averageBytes = report.DiskBytesBefore / int64(len(entries))
	}
	for i := range eligible {
		if eligible[i].Bytes <= 0 {
			eligible[i].Bytes = averageBytes
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		mi, mj := eligible[i].Reason == "model-run-retention", eligible[j].Reason == "model-run-retention"
		if mi != mj {
			return mi
		}
		if eligible[i].UpdatedAt.Equal(eligible[j].UpdatedAt) {
			return eligible[i].Key < eligible[j].Key
		}
		return eligible[i].UpdatedAt.Before(eligible[j].UpdatedAt)
	})
	selected := map[string]sessionMaintenanceVictim{}
	for _, victim := range eligible {
		if victim.Reason != "" {
			selected[victim.Key] = victim
		}
	}

	remaining := len(entries) - len(selected)
	if cfg.MaxEntries > 0 && shouldRunEntryMaintenance(len(entries), cfg.MaxEntries, force) && remaining > cfg.MaxEntries {
		for _, victim := range eligible {
			if remaining <= cfg.MaxEntries {
				break
			}
			if _, exists := selected[victim.Key]; exists {
				continue
			}
			victim.Reason = "entry-cap"
			selected[victim.Key] = victim
			remaining--
		}
	}
	if cfg.MaxDiskBytes > 0 && report.DiskBytesBefore > cfg.MaxDiskBytes {
		projected := report.DiskBytesBefore
		for _, victim := range eligible {
			if projected <= cfg.HighWaterBytes {
				break
			}
			if _, exists := selected[victim.Key]; exists {
				projected -= victim.Bytes
				continue
			}
			victim.Reason = "disk-budget"
			selected[victim.Key] = victim
			projected -= victim.Bytes
		}
	}
	for _, victim := range selected {
		report.Candidates = append(report.Candidates, victim)
	}
	sort.SliceStable(report.Candidates, func(i, j int) bool { return report.Candidates[i].UpdatedAt.Before(report.Candidates[j].UpdatedAt) })
	if cfg.Mode == "warn" {
		report.DiskBytesAfter = report.DiskBytesBefore
		return report, nil
	}
	for _, victim := range report.Candidates {
		archive, err := s.archiveAndDelete(ctx, victim, protected)
		if err != nil {
			report.Skipped = append(report.Skipped, victim.Key)
			log.Printf("session maintenance skip key=%s reason=%s: %v", victim.Key, victim.Reason, err)
			continue
		}
		report.Deleted = append(report.Deleted, victim.Key)
		if archive != "" {
			report.Archived = append(report.Archived, archive)
		}
	}
	s.sweepArtifacts(cfg, now)
	report.DiskBytesAfter = sessionMaintenanceDiskUsage(s.sessions.Path(), defaultSessionArtifactsRoot())
	return report, nil
}

func (s *sessionMaintenanceService) protectedSet(entries map[string]state.SessionEntry, cfg state.ResolvedSessionMaintenanceConfig, now time.Time) map[string]struct{} {
	out := map[string]struct{}{}
	if s.protected != nil {
		for key := range s.protected() {
			out[key] = struct{}{}
		}
	}
	for key, entry := range entries {
		updated := entry.UpdatedAt
		if updated.IsZero() {
			updated = entry.CreatedAt
		}
		keyLower := strings.ToLower(key)
		if entry.Archived || entry.ActiveTaskID != "" || entry.ActiveRunID != "" || keyLower == "main" || keyLower == "global" {
			out[key] = struct{}{}
			continue
		}
		if cfg.PreserveRecentSeconds > 0 && now.Sub(updated) < time.Duration(cfg.PreserveRecentSeconds)*time.Second {
			out[key] = struct{}{}
		}
	}
	return out
}

func (s *sessionMaintenanceService) archiveAndDelete(ctx context.Context, victim sessionMaintenanceVictim, protected map[string]struct{}) (string, error) {
	var archivePath string
	err := withExclusiveSessionTurn(ctx, victim.SessionID, 500*time.Millisecond, func() error {
		if _, ok := protected[victim.Key]; ok {
			return fmt.Errorf("session became protected")
		}
		entry, ok := s.sessions.Get(victim.Key)
		if !ok {
			return nil
		}
		if entry.ActiveTaskID != "" || entry.ActiveRunID != "" || entry.Archived {
			return fmt.Errorf("session is active or archived")
		}
		all, err := s.transcripts.ListSessionAllBranches(ctx, victim.SessionID)
		if err != nil {
			return err
		}
		archivePath, err = archiveTranscriptSnapshot(victim.SessionID, "maintenance:"+victim.Reason, all, s.now(), defaultSessionArchiveDir())
		if err != nil {
			return err
		}
		for _, item := range all {
			if err := s.transcripts.DeleteEntry(ctx, victim.SessionID, item.EntryID); err != nil {
				return err
			}
		}
		for _, path := range []string{entry.SessionFile, entry.SessionMemoryFile, sessionTranscriptPath(victim.SessionID)} {
			_ = removeOwnedSessionArtifact(path)
		}
		if err := s.sessions.Delete(victim.Key); err != nil {
			return err
		}
		current, getErr := s.docs.GetSession(ctx, victim.SessionID)
		if getErr == nil {
			if _, updateErr := updateExistingSessionDoc(ctx, s.docs, victim.SessionID, current.PeerPubKey, func(doc *state.SessionDoc) error {
				doc.Meta = mergeSessionMeta(doc.Meta, map[string]any{"deleted": true, "deleted_at": s.now().Unix(), "prune_reason": victim.Reason, "archive_path": archivePath})
				return nil
			}); updateErr != nil {
				log.Printf("session maintenance tombstone failed key=%s: %v", victim.Key, updateErr)
			}
		}
		return nil
	})
	return archivePath, err
}

func shouldRunEntryMaintenance(count, maxEntries int, force bool) bool {
	if force {
		return count > maxEntries
	}
	if maxEntries <= 49 {
		return count > maxEntries
	}
	slack := maxEntries / 10
	if slack < 25 {
		slack = 25
	}
	return count >= maxEntries+slack
}
func firstSessionID(key, id string) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	return key
}
func isShortModelRunSession(key string, entry state.SessionEntry) bool {
	value := strings.ToLower(key + " " + entry.SpawnedBy)
	return strings.Contains(value, "model-run") || strings.Contains(value, ":probe:") || strings.Contains(value, ":heartbeat:") || strings.Contains(value, ":cron:") || strings.Contains(value, ":subagent:")
}
func sessionEntryLocalBytes(entry state.SessionEntry) int64 {
	var total int64
	for _, path := range []string{entry.SessionFile, entry.SessionMemoryFile} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			total += info.Size()
		}
	}
	return total
}
func sessionMaintenanceDiskUsage(storePath, artifactRoot string) int64 {
	var total int64
	for _, path := range []string{storePath, storePath + ".journal", artifactRoot} {
		_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil || d == nil {
				return nil
			}
			if d.Type()&os.ModeSymlink != 0 {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if !d.IsDir() {
				if info, statErr := d.Info(); statErr == nil {
					total += info.Size()
				}
			}
			return nil
		})
	}
	return total
}
func removeOwnedSessionArtifact(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root, _ := filepath.Abs(defaultSessionArtifactsRoot())
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refuse artifact outside session root")
	}
	if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
func (s *sessionMaintenanceService) sweepArtifacts(cfg state.ResolvedSessionMaintenanceConfig, now time.Time) {
	if cfg.ArchiveRetentionSeconds <= 0 {
		return
	}
	cutoff := now.Add(-time.Duration(cfg.ArchiveRetentionSeconds) * time.Second)
	_ = filepath.WalkDir(defaultSessionArchiveDir(), func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if info, e := d.Info(); e == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}
