package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"metiq/internal/config"
	"metiq/internal/policy"
)

func runSecurityDoctor(args []string) error {
	fs := flag.NewFlagSet("security doctor", flag.ContinueOnError)
	var configPath string
	var jsonOut bool
	fs.StringVar(&configPath, "config", "", "config path")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(configPath) == "" {
		p, err := config.DefaultConfigPath()
		if err != nil {
			return err
		}
		configPath = p
	}
	doc, err := config.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("load config for security doctor: %w", err)
	}
	report := policy.CheckPolicyConformance(doc)
	if jsonOut {
		pass, warn, fail := report.Summary()
		return printJSON(map[string]any{"config_path": configPath, "summary": map[string]int{"pass": pass, "warn": warn, "fail": fail}, "findings": report.Findings})
	}
	fmt.Printf("Security policy doctor: %s\n\n", configPath)
	for _, f := range report.Findings {
		switch f.Status {
		case policy.ConformanceFail:
			printError("  ✗ fail %s: %s", f.ID, f.Message)
		case policy.ConformanceWarn:
			printWarn("  ! warn %s: %s", f.ID, f.Message)
		default:
			printSuccess("  ✓ pass %s: %s", f.ID, f.Message)
		}
		if f.Remediation != "" {
			printMuted("    → %s", f.Remediation)
		}
	}
	pass, warn, fail := report.Summary()
	fmt.Println()
	if fail > 0 {
		return fmt.Errorf("security doctor found %d fail(s), %d warn(s), %d pass", fail, warn, pass)
	}
	if warn > 0 {
		fmt.Fprintf(os.Stderr, "security doctor found %d warn(s), %d pass\n", warn, pass)
		return nil
	}
	printSuccess("All security policy checks passed.")
	return nil
}
