package main

import (
	"flag"
	"fmt"
	"metiq/internal/security"
	"os"
	"sort"
)

// ─── update ───────────────────────────────────────────────────────────────────

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("update.status", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	current := stringField(result, "current_version")
	latest := stringField(result, "latest_version")
	hasUpdate, _ := result["has_update"].(bool)

	fmt.Printf("current: %s\n", current)
	fmt.Printf("latest:  %s\n", latest)
	if hasUpdate {
		fmt.Printf("update available — run: curl -fsSL https://raw.githubusercontent.com/metiq/metiq/main/scripts/install.sh | bash\n")
	} else {
		fmt.Println("up to date")
	}
	return nil
}

// ─── security ────────────────────────────────────────────────────────────────

func runSecurity(args []string) error {
	if len(args) == 0 {
		return runSecurityAudit(nil)
	}
	switch args[0] {
	case "audit":
		return runSecurityAudit(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "security subcommands: audit\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runSecurityAudit(args []string) error {
	fs := flag.NewFlagSet("security audit", flag.ContinueOnError)
	var bootstrapPath, configPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&configPath, "config", "", "live config path")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	findings := collectSecurityFindings(bootstrapPath, configPath)

	if jsonOut {
		return printJSON(map[string]any{"findings": findings})
	}

	if len(findings) == 0 {
		fmt.Println("✓ No security issues found")
		return nil
	}

	severityOrder := map[string]int{"critical": 0, "warn": 1, "info": 2}
	sort.Slice(findings, func(i, j int) bool {
		si := severityOrder[findings[i].Severity]
		sj := severityOrder[findings[j].Severity]
		if si != sj {
			return si < sj
		}
		return findings[i].CheckID < findings[j].CheckID
	})

	critical := 0
	warns := 0
	for _, f := range findings {
		icon := "·"
		switch f.Severity {
		case "critical":
			icon = "✗"
			critical++
		case "warn":
			icon = "!"
			warns++
		}
		fmt.Printf("%s [%s] %s: %s\n", icon, f.Severity, f.CheckID, f.Message)
		if f.Remediation != "" {
			fmt.Printf("  → %s\n", f.Remediation)
		}
	}

	fmt.Printf("\n%d findings (%d critical, %d warn)\n", len(findings), critical, warns)
	if critical > 0 {
		return fmt.Errorf("security audit failed: %d critical issue(s)", critical)
	}
	return nil
}

// securityFinding aliases the security package type for CLI rendering.
type securityFinding = security.Finding

// collectSecurityFindings runs security checks using the internal security package.
func collectSecurityFindings(bootstrapPath, _ string) []securityFinding {
	report := security.Audit(security.AuditOptions{
		BootstrapPath: bootstrapPath,
	})
	return report.Findings
}

// ─── health ───────────────────────────────────────────────────────────────────

func runHealth(args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.get("/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "daemon unreachable:", err)
		os.Exit(1)
	}
	if ok, _ := result["ok"].(bool); ok {
		fmt.Println("ok")
	} else {
		fmt.Fprintln(os.Stderr, "daemon returned unhealthy status")
		os.Exit(1)
	}
	return nil
}
