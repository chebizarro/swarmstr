package memory

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteRecoveryUsesWALAndCleanShutdownDeletesJournal(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".metiq", "memory.sqlite")
	b, err := OpenSQLiteBackendWithRecoveryOptions(dbPath, SQLiteRecoveryOptions{BackupEnabled: false, BackupEnabledSet: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	var mode string
	if err := b.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal mode: %v", err)
	}
	if !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal mode = %q, want WAL", mode)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db, err := sql.Open("sqlite", "file:"+dbPath+"?_busy_timeout=5000")
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer db.Close()
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("post-close journal mode: %v", err)
	}
	if !strings.EqualFold(mode, "delete") {
		t.Fatalf("post-close journal mode = %q, want DELETE", mode)
	}
}

func TestSQLiteWeeklyBackupsRetainLastFourByDefault(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".metiq", "memory.sqlite")
	now := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	b, err := OpenSQLiteBackendWithRecoveryOptions(dbPath, SQLiteRecoveryOptions{
		BackupEnabled:        true,
		BackupEnabledSet:     true,
		BackupRetentionWeeks: 4,
		Now: func() time.Time {
			return now
		},
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer b.Close()
	for i := 0; i < 6; i++ {
		now = time.Date(2026, 1, 5+i*7, 12, 0, 0, 0, time.UTC)
		if _, err := b.CreateWeeklyBackupNow(); err != nil {
			t.Fatalf("backup week %d: %v", i, err)
		}
	}
	backups, err := listSQLiteBackups(filepath.Join(filepath.Dir(dbPath), sqliteBackupDirName))
	if err != nil {
		t.Fatalf("list backups: %v", err)
	}
	if len(backups) != 4 {
		t.Fatalf("backup count = %d, want 4: %v", len(backups), backups)
	}
	for _, backup := range backups {
		if err := checkSQLiteIntegrity(backup); err != nil {
			t.Fatalf("backup %s failed integrity: %v", backup, err)
		}
	}
}

func TestSQLiteStartupIntegrityCheckDoesNotQuarantineNonCorruptionErrors(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), ".metiq", "memory.sqlite")
	if err := os.MkdirAll(dbPath, 0o700); err != nil {
		t.Fatalf("mkdir db path: %v", err)
	}
	_, err := OpenSQLiteBackendWithRecoveryOptions(dbPath, SQLiteRecoveryOptions{BackupEnabled: false, BackupEnabledSet: true})
	if err == nil {
		t.Fatalf("expected open error for directory db path")
	}
	if _, statErr := os.Stat(dbPath); statErr != nil {
		t.Fatalf("non-corruption integrity error should leave path untouched: %v", statErr)
	}
	matches, _ := filepath.Glob(dbPath + ".corrupted.*")
	if len(matches) != 0 {
		t.Fatalf("non-corruption error was quarantined: %v", matches)
	}
}

func TestSQLiteCorruptionQuarantineRestoresLatestBackupAndLogsStacktrace(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".metiq", "memory.sqlite")
	now := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	var logs []string
	opts := SQLiteRecoveryOptions{
		BackupEnabled:        true,
		BackupEnabledSet:     true,
		BackupRetentionWeeks: 4,
		Now:                  func() time.Time { return now },
		Logf: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	}
	b, err := OpenSQLiteBackendWithRecoveryOptions(dbPath, opts)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	rec := MemoryRecord{ID: "restore-me", Type: MemoryRecordTypeDecision, Scope: MemoryRecordScopeProject, Subject: "restore", Text: "restore this record from backup"}
	if err := b.WriteMemoryRecord(context.Background(), rec); err != nil {
		t.Fatalf("write record: %v", err)
	}
	now = now.AddDate(0, 0, 7)
	if _, err := b.CreateWeeklyBackupNow(); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("corrupt db: %v", err)
	}
	now = now.Add(time.Hour)
	b, err = OpenSQLiteBackendWithRecoveryOptions(dbPath, opts)
	if err != nil {
		t.Fatalf("reopen after corruption: %v", err)
	}
	defer b.Close()
	if _, found, err := b.GetMemoryRecord(context.Background(), "restore-me"); err != nil || !found {
		t.Fatalf("restored record found=%v err=%v", found, err)
	}
	matches, _ := filepath.Glob(dbPath + ".corrupted.*")
	if len(matches) == 0 {
		t.Fatalf("corrupted database was not quarantined")
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "stacktrace=") || !strings.Contains(joined, "memory sqlite corruption detected") {
		t.Fatalf("corruption log missing stacktrace: %s", joined)
	}
}

func TestSQLiteCorruptionRebuildsFromMarkdownAndSessionSummariesWithoutBackup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, ".metiq", "memory.sqlite")
	workspaceDir := filepath.Join(dir, "workspace")
	durableRoot := filepath.Join(workspaceDir, ".metiq", "agent-memory", "main")
	_, err := WriteDurableMemoryFile(durableRoot, MemoryRecord{
		ID:      "durable-rebuild",
		Type:    MemoryRecordTypeDecision,
		Scope:   MemoryRecordScopeProject,
		Subject: "durable rebuild",
		Text:    "rebuild this durable markdown memory",
	})
	if err != nil {
		t.Fatalf("write durable file: %v", err)
	}
	if _, err := WriteSessionMemoryFile(workspaceDir, "session-1", strings.ReplaceAll(DefaultSessionMemoryTemplate, "_What is actively being worked on right now? Pending tasks not yet completed. Immediate next steps._", "_What is actively being worked on right now? Pending tasks not yet completed. Immediate next steps._\nRecovery test session summary.")); err != nil {
		t.Fatalf("write session memory: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatalf("mkdir db dir: %v", err)
	}
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("corrupt db: %v", err)
	}
	b, err := OpenSQLiteBackendWithRecoveryOptions(dbPath, SQLiteRecoveryOptions{
		BackupEnabled:       false,
		BackupEnabledSet:    true,
		RebuildDurableRoots: []string{filepath.Join(workspaceDir, ".metiq", "agent-memory")},
		RebuildWorkspaceDir: workspaceDir,
		Now: func() time.Time {
			return time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatalf("open after corruption: %v", err)
	}
	defer b.Close()
	if _, found, err := b.GetMemoryRecord(context.Background(), "durable-rebuild"); err != nil || !found {
		t.Fatalf("durable rebuild record found=%v err=%v", found, err)
	}
	var summaries int
	if err := b.db.QueryRow(`SELECT COUNT(*) FROM memory_records WHERE source_kind = ?`, MemorySourceKindSessionSummary).Scan(&summaries); err != nil {
		t.Fatalf("count summaries: %v", err)
	}
	if summaries == 0 {
		t.Fatalf("expected session summary records to be rebuilt")
	}
}
