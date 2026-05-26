package main

import (
	"archive/zip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"metiq/internal/config"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runBackup(args []string) error {
	if len(args) == 0 {
		return runBackupCreate(nil)
	}
	switch args[0] {
	case "create", "export":
		return runBackupCreate(args[1:])
	case "restore", "import":
		return runBackupRestore(args[1:])
	default:
		return fmt.Errorf("backup subcommands: create, restore")
	}
}

func runBackupCreate(args []string) error {
	fs := flag.NewFlagSet("backup create", flag.ContinueOnError)
	var outPath, configPath, bootstrapPath string
	var includeBootstrap, jsonOut bool
	fs.StringVar(&outPath, "out", "", "backup zip path")
	fs.StringVar(&configPath, "config", "", "config file path")
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap file path")
	fs.BoolVar(&includeBootstrap, "include-bootstrap", true, "include bootstrap.json when present")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if configPath == "" {
		var err error
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return err
		}
	}
	if bootstrapPath == "" && includeBootstrap {
		if p, err := config.DefaultBootstrapPath(); err == nil {
			bootstrapPath = p
		}
	}
	if outPath == "" {
		outPath = fmt.Sprintf("metiq-backup-%s.zip", time.Now().UTC().Format("20060102T150405Z"))
	}
	files := []backupFile{{Source: configPath, Name: "metiq/config.json", Required: true}}
	if includeBootstrap && strings.TrimSpace(bootstrapPath) != "" {
		files = append(files, backupFile{Source: bootstrapPath, Name: "metiq/bootstrap.json"})
	}
	written, err := createBackupZip(outPath, files)
	if err != nil {
		return err
	}
	result := map[string]any{"ok": true, "path": outPath, "files": written, "count": len(written)}
	if jsonOut {
		return printJSON(result)
	}
	printSuccess("backup created: %s", outPath)
	for _, name := range written {
		printMuted("  %s", name)
	}
	return nil
}

type backupFile struct {
	Source   string
	Name     string
	Required bool
}

func createBackupZip(outPath string, files []backupFile) ([]string, error) {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil && filepath.Dir(outPath) != "." {
		return nil, err
	}
	out, err := os.Create(outPath)
	if err != nil {
		return nil, err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()

	manifest := map[string]any{"created_at": time.Now().UTC().Format(time.RFC3339), "version": version}
	manifestRaw, _ := json.MarshalIndent(manifest, "", "  ")
	mw, err := zw.Create("metiq-backup/manifest.json")
	if err != nil {
		return nil, err
	}
	if _, err := mw.Write(manifestRaw); err != nil {
		return nil, err
	}
	written := []string{"metiq-backup/manifest.json"}

	for _, f := range files {
		if strings.TrimSpace(f.Source) == "" {
			continue
		}
		in, err := os.Open(f.Source)
		if err != nil {
			if os.IsNotExist(err) && !f.Required {
				continue
			}
			return nil, err
		}
		func() {
			defer in.Close()
			w, createErr := zw.Create(f.Name)
			if createErr != nil {
				err = createErr
				return
			}
			_, err = io.Copy(w, in)
		}()
		if err != nil {
			return nil, err
		}
		written = append(written, f.Name)
	}
	return written, nil
}

func runBackupRestore(args []string) error {
	fs := flag.NewFlagSet("backup restore", flag.ContinueOnError)
	var inPath, targetDir string
	var dryRun, yes, jsonOut bool
	fs.StringVar(&inPath, "file", "", "backup zip path")
	fs.StringVar(&targetDir, "target-dir", "", "target metiq directory (default ~/.metiq)")
	fs.BoolVar(&dryRun, "dry-run", false, "list files without writing")
	fs.BoolVar(&yes, "yes", false, "confirm restore overwrite")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if inPath == "" && fs.NArg() > 0 {
		inPath = fs.Arg(0)
	}
	if inPath == "" {
		return fmt.Errorf("usage: metiq backup restore --file <backup.zip> [--target-dir dir] --yes")
	}
	if targetDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		targetDir = filepath.Join(home, ".metiq")
	}
	if !dryRun && !yes {
		return fmt.Errorf("restore overwrites files; pass --yes to confirm or --dry-run to inspect")
	}
	restored, err := restoreBackupZip(inPath, targetDir, dryRun)
	if err != nil {
		return err
	}
	result := map[string]any{"ok": true, "dry_run": dryRun, "target_dir": targetDir, "files": restored, "count": len(restored)}
	if jsonOut {
		return printJSON(result)
	}
	if dryRun {
		printInfo("backup restore dry run: %s", inPath)
	} else {
		printSuccess("backup restored to: %s", targetDir)
	}
	for _, name := range restored {
		printMuted("  %s", name)
	}
	return nil
}

func restoreBackupZip(inPath, targetDir string, dryRun bool) ([]string, error) {
	zr, err := zip.OpenReader(inPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var restored []string
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "metiq/")
		if name == f.Name || name == "" || strings.Contains(name, "..") || filepath.IsAbs(name) {
			continue
		}
		target := filepath.Join(targetDir, filepath.FromSlash(name))
		cleanTarget := filepath.Clean(target)
		cleanRoot := filepath.Clean(targetDir) + string(os.PathSeparator)
		if !strings.HasPrefix(cleanTarget, cleanRoot) && cleanTarget != filepath.Clean(targetDir) {
			return nil, fmt.Errorf("unsafe backup entry %q", f.Name)
		}
		restored = append(restored, cleanTarget)
		if dryRun {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(cleanTarget), 0o700); err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		func() {
			defer rc.Close()
			out, createErr := os.OpenFile(cleanTarget, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if createErr != nil {
				err = createErr
				return
			}
			defer out.Close()
			_, err = io.Copy(out, rc)
		}()
		if err != nil {
			return nil, err
		}
	}
	return restored, nil
}
