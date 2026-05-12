package main

import (
	"flag"
	"fmt"
)

// ─── status ───────────────────────────────────────────────────────────────────

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
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

	result, err := cl.get("/status")
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	pubkey := stringField(result, "pubkey")
	uptime := floatField(result, "uptime_seconds")
	dmPolicy := stringField(result, "dm_policy")
	ver := stringField(result, "version")

	fmt.Printf("● metiqd running\n")
	fmt.Printf("  pubkey:    %s\n", pubkey)
	fmt.Printf("  version:   %s\n", ver)
	fmt.Printf("  uptime:    %.0fs\n", uptime)
	fmt.Printf("  dm_policy: %s\n", dmPolicy)

	if relays, ok := result["relays"].([]any); ok {
		fmt.Printf("  relays:    %d\n", len(relays))
		for _, r := range relays {
			fmt.Printf("             %v\n", r)
		}
	}
	return nil
}

// ─── version ─────────────────────────────────────────────────────────────────

func runVersion(_ []string) error {
	printVersion(version)
	return nil
}

// ─── logs ────────────────────────────────────────────────────────────────────

func runLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var lines int
	var level string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.IntVar(&lines, "lines", 50, "number of recent log lines to show")
	fs.StringVar(&level, "level", "", "filter by log level (debug|info|warn|error)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("logs.tail", map[string]any{
		"lines": lines,
		"level": level,
	})
	if err != nil {
		return err
	}
	return printJSON(result)
}
