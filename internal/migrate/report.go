package migrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteReport writes both JSON and Markdown reports to the target directory.
func WriteReport(report *Report, targetDir string) error {
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Write JSON report
	jsonPath := filepath.Join(targetDir, "migration_report.json")
	jsonData, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON report: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write JSON report: %w", err)
	}

	// Write Markdown report
	mdPath := filepath.Join(targetDir, "MIGRATION_REPORT.md")
	mdContent := FormatMarkdownReport(report)
	if err := os.WriteFile(mdPath, []byte(mdContent), 0644); err != nil {
		return fmt.Errorf("failed to write Markdown report: %w", err)
	}

	return nil
}

// FormatMarkdownReport generates a human-readable Markdown report.
func FormatMarkdownReport(r *Report) string {
	var sb strings.Builder

	// Header
	sb.WriteString("# Migration Report: OpenClaw → Metiq\n\n")

	// Summary
	status := "✅ SUCCESS"
	if !r.Success {
		status = "❌ FAILED"
	}
	sb.WriteString(fmt.Sprintf("**Status**: %s\n\n", status))

	sb.WriteString("## Summary\n\n")
	sb.WriteString(fmt.Sprintf("| Field | Value |\n"))
	sb.WriteString(fmt.Sprintf("|-------|-------|\n"))
	sb.WriteString(fmt.Sprintf("| Source Directory | `%s` |\n", r.SourceDir))
	sb.WriteString(fmt.Sprintf("| Target Directory | `%s` |\n", r.TargetDir))
	if r.SourceAgent != "" {
		sb.WriteString(fmt.Sprintf("| Source Agent | %s |\n", r.SourceAgent))
	}
	sb.WriteString(fmt.Sprintf("| Migration Date | %s |\n", r.MigrationDate.Format("2006-01-02 15:04:05 UTC")))
	sb.WriteString(fmt.Sprintf("| Phase | %s |\n", r.Phase))
	sb.WriteString(fmt.Sprintf("| Duration | %dms |\n", r.DurationMs))
	sb.WriteString("\n")

	// Artifacts
	sb.WriteString("## Artifacts\n\n")

	// Group by action
	actionGroups := map[ArtifactAction][]ArtifactEntry{}
	for _, art := range r.Artifacts {
		actionGroups[art.Action] = append(actionGroups[art.Action], art)
	}

	actionOrder := []ArtifactAction{ActionPreserve, ActionMigrate, ActionTransform, ActionConvert, ActionOmit}
	actionEmoji := map[ArtifactAction]string{
		ActionPreserve:  "🔒",
		ActionMigrate:   "📦",
		ActionTransform: "🔄",
		ActionConvert:   "⚙️",
		ActionOmit:      "🗑️",
	}

	for _, action := range actionOrder {
		arts, ok := actionGroups[action]
		if !ok || len(arts) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("### %s %s (%d)\n\n", actionEmoji[action], strings.Title(string(action)), len(arts)))

		sb.WriteString("| Type | Source | Target | Description |\n")
		sb.WriteString("|------|--------|--------|-------------|\n")

		for _, art := range arts {
			src := art.SourcePath
			if src == "" {
				src = "-"
			} else {
				src = fmt.Sprintf("`%s`", filepath.Base(src))
			}

			tgt := art.TargetPath
			if tgt == "" {
				tgt = "-"
			} else {
				tgt = fmt.Sprintf("`%s`", filepath.Base(tgt))
			}

			desc := art.Description
			if desc == "" {
				desc = "-"
			}

			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", art.Type, src, tgt, desc))
		}
		sb.WriteString("\n")
	}

	// Omitted paths
	if len(r.OmittedPaths) > 0 {
		sb.WriteString("### Runtime State (Discarded)\n\n")
		sb.WriteString("The following runtime directories were intentionally omitted:\n\n")
		for _, p := range r.OmittedPaths {
			sb.WriteString(fmt.Sprintf("- `%s/`\n", p))
		}
		sb.WriteString("\n")
	}

	// Issues
	if len(r.Issues) > 0 {
		sb.WriteString("## Issues\n\n")

		// Count by severity
		counts := map[Severity]int{}
		for _, issue := range r.Issues {
			counts[issue.Severity]++
		}

		if counts[SeverityError] > 0 {
			sb.WriteString(fmt.Sprintf("- ❌ **Errors**: %d\n", counts[SeverityError]))
		}
		if counts[SeverityWarning] > 0 {
			sb.WriteString(fmt.Sprintf("- ⚠️ **Warnings**: %d\n", counts[SeverityWarning]))
		}
		if counts[SeverityInfo] > 0 {
			sb.WriteString(fmt.Sprintf("- ℹ️ **Info**: %d\n", counts[SeverityInfo]))
		}
		sb.WriteString("\n")

		// Manual review items
		manualReview := []Issue{}
		for _, issue := range r.Issues {
			if issue.ManualReview {
				manualReview = append(manualReview, issue)
			}
		}

		if len(manualReview) > 0 {
			sb.WriteString("### 🔍 Manual Review Required\n\n")
			for _, issue := range manualReview {
				sb.WriteString(fmt.Sprintf("- **%s**: %s\n", issue.Path, issue.Message))
				if issue.Suggestion != "" {
					sb.WriteString(fmt.Sprintf("  - 💡 %s\n", issue.Suggestion))
				}
			}
			sb.WriteString("\n")
		}

		// All issues table
		sb.WriteString("### All Issues\n\n")
		sb.WriteString("| Severity | Phase | Path | Message |\n")
		sb.WriteString("|----------|-------|------|----------|\n")

		severityEmoji := map[Severity]string{
			SeverityError:   "❌",
			SeverityWarning: "⚠️",
			SeverityInfo:    "ℹ️",
		}

		for _, issue := range r.Issues {
			path := issue.Path
			if path == "" {
				path = "-"
			}
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
				severityEmoji[issue.Severity], issue.Phase, path, issue.Message))
		}
		sb.WriteString("\n")
	}

	// Next steps
	sb.WriteString("## Next Steps\n\n")
	sb.WriteString("1. **Review bootstrap.json**: Update relay URLs for your environment\n")
	sb.WriteString("2. **Verify secrets**: Check that `.env` variables are correctly referenced\n")
	sb.WriteString("3. **Test configuration**: Run `metiq config validate`\n")
	sb.WriteString("4. **Start daemon**: Run `metiqd` to verify the agent starts correctly\n")

	if len(r.Issues) > 0 {
		hasManualReview := false
		for _, issue := range r.Issues {
			if issue.ManualReview {
				hasManualReview = true
				break
			}
		}
		if hasManualReview {
			sb.WriteString("5. **Address manual review items**: See the issues above\n")
		}
	}

	sb.WriteString("\n---\n")
	sb.WriteString(fmt.Sprintf("*Generated by metiq-migrator on %s*\n", r.MigrationDate.Format("2006-01-02 15:04:05 UTC")))

	return sb.String()
}

// FormatTerminalReport returns a concise terminal-friendly summary.
func FormatTerminalReport(r *Report) string {
	var sb strings.Builder

	if r.Success {
		sb.WriteString("✅ Migration completed successfully\n\n")
	} else {
		sb.WriteString("❌ Migration failed\n\n")
	}

	sb.WriteString(fmt.Sprintf("Source: %s\n", r.SourceDir))
	sb.WriteString(fmt.Sprintf("Target: %s\n", r.TargetDir))
	sb.WriteString(fmt.Sprintf("Duration: %dms\n\n", r.DurationMs))

	// Artifact summary
	actionCounts := map[ArtifactAction]int{}
	for _, art := range r.Artifacts {
		actionCounts[art.Action]++
	}

	sb.WriteString("Artifacts:\n")
	if c := actionCounts[ActionPreserve]; c > 0 {
		sb.WriteString(fmt.Sprintf("  🔒 Preserved:   %d\n", c))
	}
	if c := actionCounts[ActionMigrate]; c > 0 {
		sb.WriteString(fmt.Sprintf("  📦 Migrated:    %d\n", c))
	}
	if c := actionCounts[ActionTransform]; c > 0 {
		sb.WriteString(fmt.Sprintf("  🔄 Transformed: %d\n", c))
	}
	if c := actionCounts[ActionConvert]; c > 0 {
		sb.WriteString(fmt.Sprintf("  ⚙️  Converted:   %d\n", c))
	}
	if c := actionCounts[ActionOmit]; c > 0 {
		sb.WriteString(fmt.Sprintf("  🗑️  Omitted:     %d\n", c))
	}

	// Issue summary
	if len(r.Issues) > 0 {
		sb.WriteString("\nIssues:\n")
		errorCount := 0
		warningCount := 0
		for _, issue := range r.Issues {
			switch issue.Severity {
			case SeverityError:
				errorCount++
			case SeverityWarning:
				warningCount++
			}
		}
		if errorCount > 0 {
			sb.WriteString(fmt.Sprintf("  ❌ Errors:   %d\n", errorCount))
		}
		if warningCount > 0 {
			sb.WriteString(fmt.Sprintf("  ⚠️  Warnings: %d\n", warningCount))
		}
	}

	sb.WriteString(fmt.Sprintf("\nReports written to:\n"))
	sb.WriteString(fmt.Sprintf("  %s/migration_report.json\n", r.TargetDir))
	sb.WriteString(fmt.Sprintf("  %s/MIGRATION_REPORT.md\n", r.TargetDir))

	return sb.String()
}
