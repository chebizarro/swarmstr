package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

// ─── cron ─────────────────────────────────────────────────────────────────────

func runCron(args []string) error {
	if len(args) == 0 {
		return runCronList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runCronList(args[1:])
	case "add":
		return runCronAdd(args[1:])
	case "remove", "rm", "delete":
		return runCronRemove(args[1:])
	case "edit":
		return runCronEdit(args[1:])
	case "enable":
		return runCronToggle(args[1:], true)
	case "disable":
		return runCronToggle(args[1:], false)
	case "show", "get":
		return runCronShow(args[1:])
	case "runs", "history":
		return runCronRuns(args[1:])
	case "run":
		return runCronRun(args[1:])
	default:
		return fmt.Errorf("unknown cron sub-command %q (list|add|edit|enable|disable|show|runs|remove|run)", args[0])
	}
}

func runCronList(args []string) error {
	fs := flag.NewFlagSet("cron list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.list", map[string]any{})
	if err != nil {
		return fmt.Errorf("cron.list: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}
	jobs, _ := result["jobs"].([]any)
	if len(jobs) == 0 {
		fmt.Println("no cron jobs configured")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSCHEDULE\tENABLED\tLAST RUN\tDESCRIPTION")
	for _, j := range jobs {
		jm, _ := j.(map[string]any)
		id := stringFieldAny(jm, "id")
		schedule := stringFieldAny(jm, "schedule")
		enabled := fmt.Sprintf("%v", jm["enabled"])
		desc := stringFieldAny(jm, "description")
		lastRun := floatFieldAny(jm, "last_run_at")
		lastRunStr := "-"
		if lastRun > 0 {
			lastRunStr = time.Unix(int64(lastRun), 0).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", id, schedule, enabled, lastRunStr, desc)
	}
	return tw.Flush()
}

func runCronAdd(args []string) error {
	fs := flag.NewFlagSet("cron add", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, id, schedule, message, agentID, method, rawParams string
	var enabled bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&id, "id", "", "cron job ID (auto-generated if empty)")
	fs.StringVar(&schedule, "schedule", "", "cron schedule expression (required); e.g. \"0 7 * * *\", \"@every 1h\", \"@daily\"")
	fs.StringVar(&message, "message", "", "agent message to send on trigger (shorthand: sets method=agent and params.text=<message>)")
	fs.StringVar(&agentID, "agent", "main", "agent ID to target (used with --message)")
	fs.StringVar(&method, "method", "", "gateway method to call on trigger (e.g. \"agent\"); required if --message not set")
	fs.StringVar(&rawParams, "params", "", "JSON params for the method (e.g. '{\"text\":\"hello\"}')")
	fs.BoolVar(&enabled, "enabled", true, "enable job immediately")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if schedule == "" {
		return fmt.Errorf("--schedule is required")
	}
	// --message is a convenience shorthand that maps to method=agent with text param.
	if message != "" && method == "" {
		method = "agent"
		params := map[string]any{"text": message}
		if agentID != "" && agentID != "main" {
			params["session_id"] = agentID
		}
		b, _ := json.Marshal(params)
		rawParams = string(b)
	}
	if method == "" {
		return fmt.Errorf("--method or --message is required")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	body := map[string]any{
		"id":       id,
		"schedule": schedule,
		"method":   method,
		"enabled":  enabled,
	}
	if rawParams != "" {
		body["params"] = json.RawMessage(rawParams)
	}
	result, err := cl.call("cron.add", body)
	if err != nil {
		return fmt.Errorf("cron.add: %w", err)
	}
	return printJSON(result)
}

func runCronEdit(args []string) error {
	fs := flag.NewFlagSet("cron edit", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, schedule, method, rawParams, description string
	var enable, disable bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&schedule, "schedule", "", "new cron schedule expression")
	fs.StringVar(&method, "method", "", "new gateway method to call")
	fs.StringVar(&rawParams, "params", "", "new JSON params for the method")
	fs.StringVar(&description, "description", "", "new job description")
	fs.BoolVar(&enable, "enable", false, "enable job")
	fs.BoolVar(&disable, "disable", false, "disable job")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq cron edit <job-id> [--schedule <expr>] [--method <method>] [--params '{...}'] [--enable|--disable]")
	}
	if enable && disable {
		return fmt.Errorf("use either --enable or --disable, not both")
	}
	patch := map[string]any{}
	if schedule != "" {
		patch["schedule"] = schedule
	}
	if method != "" {
		patch["method"] = method
	}
	if rawParams != "" {
		patch["params"] = json.RawMessage(rawParams)
	}
	if description != "" {
		patch["description"] = description
	}
	if enable || disable {
		patch["enabled"] = enable
	}
	if len(patch) == 0 {
		return fmt.Errorf("no changes specified")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.update", map[string]any{"id": fs.Arg(0), "patch": patch})
	if err != nil {
		return fmt.Errorf("cron.update: %w", err)
	}
	return printJSON(result)
}

func runCronToggle(args []string, enabled bool) error {
	fs := flag.NewFlagSet("cron toggle", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq cron enable|disable <job-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.update", map[string]any{"id": fs.Arg(0), "patch": map[string]any{"enabled": enabled}})
	if err != nil {
		return fmt.Errorf("cron.update: %w", err)
	}
	return printJSON(result)
}

func runCronShow(args []string) error {
	fs := flag.NewFlagSet("cron show", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq cron show <job-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.get", map[string]any{"id": fs.Arg(0)})
	if err != nil {
		return fmt.Errorf("cron.get: %w", err)
	}
	return printJSON(result)
}

func runCronRuns(args []string) error {
	fs := flag.NewFlagSet("cron runs", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var limit int
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.IntVar(&limit, "limit", 20, "maximum run entries to return")
	if err := fs.Parse(args); err != nil {
		return err
	}
	body := map[string]any{"limit": limit}
	if fs.NArg() > 0 {
		body["id"] = fs.Arg(0)
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.runs", body)
	if err != nil {
		return fmt.Errorf("cron.runs: %w", err)
	}
	return printJSON(result)
}

func runCronRemove(args []string) error {
	fs := flag.NewFlagSet("cron remove", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq cron remove <job-id>")
	}
	jobID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.remove", map[string]any{"id": jobID})
	if err != nil {
		return fmt.Errorf("cron.remove: %w", err)
	}
	return printJSON(result)
}

func runCronRun(args []string) error {
	fs := flag.NewFlagSet("cron run", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq cron run <job-id>")
	}
	jobID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("cron.run", map[string]any{"id": jobID})
	if err != nil {
		return fmt.Errorf("cron.run: %w", err)
	}
	return printJSON(result)
}
