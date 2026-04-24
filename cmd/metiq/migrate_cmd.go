package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"metiq/internal/migrate"
)

func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)

	var (
		sourceDir   string
		targetDir   string
		dryRun      bool
		apply       bool
		force       bool
		skipSecrets bool
		verbose     bool
	)

	fs.StringVar(&sourceDir, "source", "", "OpenClaw home directory (e.g., ~/.openclaw)")
	fs.StringVar(&targetDir, "target", "", "Metiq home directory (default: ~/.metiq)")
	fs.BoolVar(&dryRun, "dry-run", true, "Simulate migration without writing files (default: true)")
	fs.BoolVar(&apply, "apply", false, "Apply the migration (writes files)")
	fs.BoolVar(&force, "force", false, "Overwrite existing files in target directory")
	fs.BoolVar(&skipSecrets, "skip-secrets", false, "Don't migrate .env and secret files")
	fs.BoolVar(&verbose, "verbose", false, "Verbose output")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: metiq migrate [options] [source-dir]

Migrate an OpenClaw agent to Metiq.

This command:
  1. Audits the source OpenClaw directory
  2. Converts openclaw.json → config.json + bootstrap.json
  3. Transforms memory files (injects YAML front-matter, normalizes paths)
  4. Converts cron jobs.json
  5. Copies workspace files (excluding runtime state)
  6. Generates migration_report.json and MIGRATION_REPORT.md

Examples:
  # Dry-run (default): see what would be migrated
  metiq migrate ~/.openclaw

  # Apply migration
  metiq migrate --apply ~/.openclaw

  # Custom target directory
  metiq migrate --apply --target ~/agents/mybot ~/.openclaw

Options:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	// Handle positional argument for source
	if fs.NArg() > 0 && sourceDir == "" {
		sourceDir = fs.Arg(0)
	}

	// Validate source directory
	if sourceDir == "" {
		return fmt.Errorf("source directory required; use --source or pass as argument")
	}

	// Expand ~ in paths
	sourceDir = expandPath(sourceDir)
	if targetDir == "" {
		home, _ := os.UserHomeDir()
		targetDir = filepath.Join(home, ".metiq")
	} else {
		targetDir = expandPath(targetDir)
	}

	// --apply overrides --dry-run
	if apply {
		dryRun = false
	}

	opts := migrate.Options{
		SourceDir:   sourceDir,
		TargetDir:   targetDir,
		DryRun:      dryRun,
		Force:       force,
		SkipSecrets: skipSecrets,
		Verbose:     verbose,
	}

	fmt.Printf("🔄 OpenClaw → Metiq Migration\n\n")
	fmt.Printf("Source: %s\n", opts.SourceDir)
	fmt.Printf("Target: %s\n", opts.TargetDir)
	if opts.DryRun {
		fmt.Printf("Mode:   DRY-RUN (use --apply to write files)\n")
	} else {
		fmt.Printf("Mode:   APPLY\n")
	}
	fmt.Println()

	// Run migration
	migrator := migrate.New(opts)
	report, err := migrator.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ Migration failed: %v\n", err)
		// Still write report on failure
		if report != nil {
			_ = migrate.WriteReport(report, opts.TargetDir)
		}
		return err
	}

	// Print terminal summary
	fmt.Println(migrate.FormatTerminalReport(report))

	// Write reports (even in dry-run mode for review)
	if err := migrate.WriteReport(report, opts.TargetDir); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to write reports: %v\n", err)
	}

	if !report.Success {
		return fmt.Errorf("migration completed with errors")
	}

	return nil
}

func expandPath(path string) string {
	if len(path) > 0 && path[0] == '~' {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, path[1:])
	}
	return path
}
