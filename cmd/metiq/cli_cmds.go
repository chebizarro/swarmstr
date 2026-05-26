package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"
)

// ─── status ───────────────────────────────────────────────────────────────────

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
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
	var lines, maxBatches int
	var level, filter, waitRaw string
	var follow, jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.IntVar(&lines, "lines", 50, "number of recent log lines to show")
	fs.StringVar(&level, "level", "", "filter by log level (debug|info|warn|error)")
	fs.StringVar(&filter, "filter", "", "case-insensitive substring filter for log output")
	fs.BoolVar(&follow, "follow", false, "follow logs as newline-delimited JSON")
	fs.BoolVar(&follow, "stream", false, "alias for --follow")
	fs.StringVar(&waitRaw, "wait", "", "long-poll follow wait duration (default 15s)")
	fs.IntVar(&maxBatches, "max-batches", 0, "maximum follow batches before exiting (0 means until interrupted)")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	if follow {
		waitTimeoutMS, err := parseObserveWait(waitRaw)
		if err != nil {
			return err
		}
		if waitTimeoutMS == 0 {
			waitTimeoutMS = 15_000
		}
		timeout := time.Duration(waitTimeoutMS)*time.Millisecond + 5*time.Second
		if cl.timeout < timeout {
			cl.timeout = timeout
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		params := map[string]any{
			"include_events":  false,
			"include_logs":    true,
			"log_limit":       lines,
			"wait_timeout_ms": waitTimeoutMS,
			"max_bytes":       32 * 1024,
		}
		return streamObserve(ctx, cl, params, observeStreamOptions{MaxBatches: maxBatches, LogFilter: filter})
	}

	result, err := cl.call("logs.tail", map[string]any{
		"lines": lines,
		"level": level,
	})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	return printLogTail(result, filter)
}

func printLogTail(result map[string]any, filter string) error {
	filter = strings.ToLower(strings.TrimSpace(filter))
	for _, item := range observeItems(result["lines"]) {
		line := fmt.Sprint(item)
		if filter != "" && !strings.Contains(strings.ToLower(line), filter) {
			continue
		}
		fmt.Println(line)
	}
	return nil
}
