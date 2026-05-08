package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

const (
	sqliteBackupDirName         = "memory-backups"
	sqliteBackupFilePrefix      = "memory-index-"
	sqliteBackupFileSuffix      = ".sqlite"
	defaultBackupRetentionWeeks = 4
)

// SQLiteRecoveryOptions controls startup recovery and scheduled backup behavior.
// The zero value is production-safe: backups are enabled and the last four
// weekly backups are retained next to the SQLite database under
// .metiq/memory-backups when the database lives in .metiq.
type SQLiteRecoveryOptions struct {
	BackupEnabled        bool
	BackupEnabledSet     bool
	BackupRetentionWeeks int
	BackupDir            string
	RebuildDurableRoots  []string
	RebuildWorkspaceDir  string
	Now                  func() time.Time
	Logf                 func(format string, args ...any)
}

func defaultSQLiteRecoveryOptions() SQLiteRecoveryOptions {
	return SQLiteRecoveryOptions{
		BackupEnabled:        true,
		BackupRetentionWeeks: defaultBackupRetentionWeeks,
		Now:                  time.Now,
		Logf:                 log.Printf,
	}
}

func normalizeSQLiteRecoveryOptions(path string, opts SQLiteRecoveryOptions) SQLiteRecoveryOptions {
	def := defaultSQLiteRecoveryOptions()
	if opts.Now == nil {
		opts.Now = def.Now
	}
	if opts.Logf == nil {
		opts.Logf = def.Logf
	}
	if !opts.BackupEnabledSet && !opts.BackupEnabled {
		opts.BackupEnabled = true
	}
	if opts.BackupRetentionWeeks <= 0 {
		opts.BackupRetentionWeeks = defaultBackupRetentionWeeks
	}
	if strings.TrimSpace(opts.BackupDir) == "" {
		opts.BackupDir = defaultSQLiteBackupDir(path)
	}
	return opts
}

func defaultSQLiteBackupDir(dbPath string) string {
	dir := filepath.Dir(dbPath)
	if filepath.Base(dir) == ".metiq" {
		return filepath.Join(dir, sqliteBackupDirName)
	}
	return filepath.Join(dir, ".metiq", sqliteBackupDirName)
}

func sqliteNowUTC(opts SQLiteRecoveryOptions) time.Time {
	now := time.Now()
	if opts.Now != nil {
		now = opts.Now()
	}
	return now.UTC()
}

type sqliteCorruptionError struct{ err error }

func (e sqliteCorruptionError) Error() string { return e.err.Error() }
func (e sqliteCorruptionError) Unwrap() error { return e.err }

func checkSQLiteIntegrity(path string) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("sqlite path is a directory: %s", path)
	}
	dsn := fmt.Sprintf("file:%s?mode=rw&_busy_timeout=%d", path, sqliteBusyTimeoutMs)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return classifySQLiteIntegrityError(err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return classifySQLiteIntegrityError(err)
	}
	rows, err := db.Query(`PRAGMA integrity_check`)
	if err != nil {
		return classifySQLiteIntegrityError(err)
	}
	defer rows.Close()
	var failures []string
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return classifySQLiteIntegrityError(err)
		}
		if strings.TrimSpace(strings.ToLower(result)) != "ok" {
			failures = append(failures, result)
		}
	}
	if err := rows.Err(); err != nil {
		return classifySQLiteIntegrityError(err)
	}
	if len(failures) > 0 {
		return sqliteCorruptionError{err: fmt.Errorf("integrity_check failed: %s", strings.Join(failures, "; "))}
	}
	return nil
}

func classifySQLiteIntegrityError(err error) error {
	if err == nil {
		return nil
	}
	if sqliteErrorLooksCorrupt(err) {
		return sqliteCorruptionError{err: err}
	}
	return err
}

func sqliteErrorLooksCorrupt(err error) bool {
	msg := strings.ToLower(err.Error())
	corruptionSignals := []string{
		"database disk image is malformed",
		"file is not a database",
		"not a database",
		"database corruption",
		"malformed database schema",
		"integrity_check failed",
	}
	for _, signal := range corruptionSignals {
		if strings.Contains(msg, signal) {
			return true
		}
	}
	return false
}

func isSQLiteCorruptionError(err error) bool {
	var corruption sqliteCorruptionError
	return errors.As(err, &corruption)
}

func recoverSQLiteDatabase(ctx context.Context, path string, opts SQLiteRecoveryOptions, integrityErr error) error {
	opts.Logf("memory sqlite corruption detected path=%q err=%v stacktrace=\n%s", path, integrityErr, debug.Stack())
	quarantined, err := quarantineSQLiteDatabase(path, sqliteNowUTC(opts))
	if err != nil {
		return fmt.Errorf("quarantine corrupted sqlite database: %w", err)
	}
	if quarantined != "" {
		opts.Logf("memory sqlite corrupted database quarantined path=%q quarantine=%q", path, quarantined)
	}
	if restored, err := restoreLatestSQLiteBackup(path, opts); err != nil {
		opts.Logf("memory sqlite backup restore failed path=%q err=%v", path, err)
	} else if restored != "" {
		opts.Logf("memory sqlite restored from backup path=%q backup=%q", path, restored)
		return nil
	}
	if err := rebuildSQLiteDatabaseFromFiles(ctx, path, opts); err != nil {
		return fmt.Errorf("rebuild sqlite memory database from markdown/session summaries: %w", err)
	}
	return nil
}

func quarantineSQLiteDatabase(path string, now time.Time) (string, error) {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	stamp := now.UTC().Format("20060102T150405Z")
	quarantinePath := path + ".corrupted." + stamp
	for i := 1; ; i++ {
		if _, err := os.Stat(quarantinePath); os.IsNotExist(err) {
			break
		}
		quarantinePath = fmt.Sprintf("%s.corrupted.%s.%d", path, stamp, i)
	}
	if err := os.Rename(path, quarantinePath); err != nil {
		return "", err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		if _, err := os.Stat(sidecar); err == nil {
			_ = os.Rename(sidecar, quarantinePath+suffix)
		}
	}
	return quarantinePath, nil
}

func restoreLatestSQLiteBackup(path string, opts SQLiteRecoveryOptions) (string, error) {
	backup, err := latestSQLiteBackup(opts.BackupDir)
	if err != nil || backup == "" {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := copySQLiteFile(backup, path, 0o600); err != nil {
		return "", err
	}
	if err := checkSQLiteIntegrity(path); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("restored backup failed integrity_check: %w", err)
	}
	return backup, nil
}

func rebuildSQLiteDatabaseFromFiles(ctx context.Context, path string, opts SQLiteRecoveryOptions) error {
	b, err := openSQLiteBackendWithoutRecovery(path)
	if err != nil {
		return err
	}
	defer b.Close()
	total := 0
	seenRoots := map[string]bool{}
	for _, root := range opts.RebuildDurableRoots {
		root = strings.TrimSpace(root)
		if root == "" || seenRoots[root] {
			continue
		}
		seenRoots[root] = true
		count, err := IngestDurableMemoryFiles(ctx, b, root)
		if err != nil {
			opts.Logf("memory sqlite durable rebuild warning root=%q err=%v", root, err)
			continue
		}
		total += count
	}
	if workspaceDir := strings.TrimSpace(opts.RebuildWorkspaceDir); workspaceDir != "" {
		count, err := IngestSessionMemoryFiles(ctx, b, workspaceDir, "")
		if err != nil {
			opts.Logf("memory sqlite session-summary rebuild warning workspace=%q err=%v", workspaceDir, err)
		} else {
			total += count
		}
	}
	if total > 0 {
		opts.Logf("memory sqlite rebuilt from markdown/session summaries path=%q records=%d", path, total)
	} else {
		opts.Logf("memory sqlite rebuilt empty database path=%q: no backup or markdown/session summaries found", path)
	}
	return nil
}

func (b *SQLiteBackend) backupIfDue() error {
	if b == nil || b.db == nil || !b.recovery.BackupEnabled {
		return nil
	}
	now := sqliteNowUTC(b.recovery)
	backupPath := sqliteBackupPathForWeek(b.recovery.BackupDir, now)
	if _, err := os.Stat(backupPath); err == nil {
		return pruneSQLiteBackups(b.recovery.BackupDir, b.recovery.BackupRetentionWeeks)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return b.createBackup(backupPath)
}

func (b *SQLiteBackend) CreateWeeklyBackupNow() (string, error) {
	if b == nil || b.db == nil {
		return "", fmt.Errorf("sqlite backend is closed")
	}
	if !b.recovery.BackupEnabled {
		return "", nil
	}
	backupPath := sqliteBackupPathForWeek(b.recovery.BackupDir, sqliteNowUTC(b.recovery))
	if err := b.createBackup(backupPath); err != nil {
		return "", err
	}
	return backupPath, nil
}

func (b *SQLiteBackend) createBackup(backupPath string) error {
	backupPath = strings.TrimSpace(backupPath)
	if backupPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(backupPath); err == nil {
		return pruneSQLiteBackups(filepath.Dir(backupPath), b.recovery.BackupRetentionWeeks)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, err := b.db.Exec(`PRAGMA wal_checkpoint(FULL)`); err != nil {
		return fmt.Errorf("checkpoint before backup: %w", err)
	}
	if _, err := b.db.Exec(`VACUUM INTO ` + sqliteQuoteLiteral(backupPath)); err != nil {
		return fmt.Errorf("vacuum into backup: %w", err)
	}
	if err := os.Chmod(backupPath, 0o600); err != nil {
		return err
	}
	if err := pruneSQLiteBackups(filepath.Dir(backupPath), b.recovery.BackupRetentionWeeks); err != nil {
		return err
	}
	b.recovery.Logf("memory sqlite weekly backup written path=%q backup=%q", b.path, backupPath)
	return nil
}

func sqliteBackupPathForWeek(dir string, now time.Time) string {
	year, week := now.UTC().ISOWeek()
	name := fmt.Sprintf("%s%04d-W%02d%s", sqliteBackupFilePrefix, year, week, sqliteBackupFileSuffix)
	return filepath.Join(dir, name)
}

func latestSQLiteBackup(dir string) (string, error) {
	backups, err := listSQLiteBackups(dir)
	if err != nil || len(backups) == 0 {
		return "", err
	}
	return backups[len(backups)-1], nil
}

func pruneSQLiteBackups(dir string, retentionWeeks int) error {
	if retentionWeeks <= 0 {
		retentionWeeks = defaultBackupRetentionWeeks
	}
	backups, err := listSQLiteBackups(dir)
	if err != nil {
		return err
	}
	for len(backups) > retentionWeeks {
		_ = os.Remove(backups[0])
		backups = backups[1:]
	}
	return nil
}

func listSQLiteBackups(dir string) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var backups []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, sqliteBackupFilePrefix) && strings.HasSuffix(name, sqliteBackupFileSuffix) {
			backups = append(backups, filepath.Join(dir, name))
		}
	}
	sort.Strings(backups)
	return backups, nil
}

func copySQLiteFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func sqliteQuoteLiteral(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "''") + "'"
}

// StartWeeklySQLiteBackups runs a low-cost daily check and writes at most one
// SQLite backup per ISO week. It is intentionally independent of memory query,
// ranking, and compaction behavior.
func StartWeeklySQLiteBackups(ctx context.Context, b *SQLiteBackend) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		if b == nil || !b.recovery.BackupEnabled {
			return
		}
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := b.backupIfDue(); err != nil {
					b.recovery.Logf("memory sqlite weekly backup failed path=%q err=%v", b.path, err)
				}
			}
		}
	}()
	return done
}
