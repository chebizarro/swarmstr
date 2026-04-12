package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// ─── sessions ─────────────────────────────────────────────────────────────────

func runSessions(args []string) error {
	if len(args) == 0 {
		return runSessionsList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runSessionsList(args[1:])
	case "get", "show":
		return runSessionsGet(args[1:])
	case "export":
		return runSessionsExport(args[1:])
	case "delete", "rm":
		return runSessionsDelete(args[1:])
	case "reset":
		return runSessionsReset(args[1:])
	case "prune":
		return runSessionsPrune(args[1:])
	default:
		return fmt.Errorf("unknown sessions sub-command %q (list|get|export|delete|reset|prune)", args[0])
	}
}

func runSessionsList(args []string) error {
	fs := flag.NewFlagSet("sessions list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var limit int
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.IntVar(&limit, "limit", 20, "max sessions to show")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("sessions.list", map[string]any{"limit": limit})
	if err != nil {
		return fmt.Errorf("sessions.list: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}
	sessions, _ := result["sessions"].([]any)
	if len(sessions) == 0 {
		fmt.Println("no sessions found")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tAGENT\tLAST INBOUND\tLAST REPLY")
	for _, s := range sessions {
		sm, _ := s.(map[string]any)
		sid := stringFieldAny(sm, "session_id")
		agentID := stringFieldAny(sm, "agent_id")
		if agentID == "" {
			agentID = "-"
		}
		lastIn := floatFieldAny(sm, "last_inbound_at")
		lastReply := floatFieldAny(sm, "last_reply_at")
		var lastInStr, lastReplyStr string
		if lastIn > 0 {
			lastInStr = time.Unix(int64(lastIn), 0).Format("2006-01-02 15:04")
		} else {
			lastInStr = "-"
		}
		if lastReply > 0 {
			lastReplyStr = time.Unix(int64(lastReply), 0).Format("2006-01-02 15:04")
		} else {
			lastReplyStr = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", sid, agentID, lastInStr, lastReplyStr)
	}
	return tw.Flush()
}

func runSessionsGet(args []string) error {
	fs := flag.NewFlagSet("sessions get", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq sessions get <session-id>")
	}
	sessionID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("session.get", map[string]any{"session_id": sessionID})
	if err != nil {
		return fmt.Errorf("session.get: %w", err)
	}
	return printJSON(result)
}

func runSessionsExport(args []string) error {
	fs := flag.NewFlagSet("sessions export", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, output, format string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&output, "output", "", "output file path (default: stdout)")
	fs.StringVar(&format, "format", "html", "export format (html)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq sessions export <session-id> [--output path]")
	}
	sessionID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("sessions.export", map[string]any{
		"session_id": sessionID,
		"format":     format,
	})
	if err != nil {
		return fmt.Errorf("sessions.export: %w", err)
	}
	html, _ := result["html"].(string)
	if html == "" {
		return fmt.Errorf("sessions.export: no content returned")
	}
	if output != "" {
		return os.WriteFile(output, []byte(html), 0o644)
	}
	fmt.Print(html)
	return nil
}

func runSessionsDelete(args []string) error {
	fs := flag.NewFlagSet("sessions delete", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq sessions delete <session-id>")
	}
	sessionID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("sessions.delete", map[string]any{"session_id": sessionID})
	if err != nil {
		return fmt.Errorf("sessions.delete: %w", err)
	}
	deleted, _ := result["deleted"].(bool)
	if deleted {
		fmt.Printf("session %s deleted\n", sessionID)
	} else {
		return printJSON(result)
	}
	return nil
}

func runSessionsReset(args []string) error {
	fs := flag.NewFlagSet("sessions reset", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq sessions reset <session-id>")
	}
	sessionID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("sessions.reset", map[string]any{"session_id": sessionID})
	if err != nil {
		return fmt.Errorf("sessions.reset: %w", err)
	}
	return printJSON(result)
}

func runSessionsPrune(args []string) error {
	fs := flag.NewFlagSet("sessions prune", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var olderThanStr string
	var dryRun, all bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&olderThanStr, "older-than", "", "delete sessions older than this duration, e.g. 7d, 30d")
	fs.BoolVar(&dryRun, "dry-run", false, "report what would be deleted without deleting")
	fs.BoolVar(&all, "all", false, "delete all sessions regardless of age")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !all && olderThanStr == "" {
		return fmt.Errorf("usage: metiq sessions prune --older-than <Nd> [--dry-run]\n  or:  metiq sessions prune --all [--dry-run]")
	}

	olderThanDays := 0
	if olderThanStr != "" {
		olderThanStr = strings.TrimSuffix(strings.TrimSpace(olderThanStr), "d")
		n, err := strconv.Atoi(olderThanStr)
		if err != nil || n <= 0 {
			return fmt.Errorf("--older-than must be a positive number of days, e.g. 7d (got %q)", olderThanStr)
		}
		olderThanDays = n
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	params := map[string]any{
		"older_than_days": olderThanDays,
		"dry_run":         dryRun,
		"all":             all,
	}
	result, err := cl.call("sessions.prune", params)
	if err != nil {
		return fmt.Errorf("sessions.prune: %w", err)
	}
	deleted, _ := result["deleted_count"].(float64)
	dryRunResult, _ := result["dry_run"].(bool)
	if dryRunResult {
		fmt.Printf("dry-run: would delete %d session(s)\n", int(deleted))
		if ids, ok := result["deleted"].([]any); ok {
			for _, id := range ids {
				fmt.Printf("  %v\n", id)
			}
		}
	} else {
		fmt.Printf("deleted %d session(s)\n", int(deleted))
	}
	return nil
}
