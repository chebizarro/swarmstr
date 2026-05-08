package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"metiq/internal/store/state"
)

// ─── tasks ────────────────────────────────────────────────────────────────────

func runTasks(args []string) error {
	if len(args) == 0 {
		return runTasksList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runTasksList(args[1:])
	case "show", "get":
		return runTasksShow(args[1:])
	case "audit", "stats":
		return runTasksAudit(args[1:])
	case "cancel":
		return runTasksCancel(args[1:])
	case "resume":
		return runTasksResume(args[1:])
	case "runs":
		return runTasksRuns(args[1:])
	default:
		return fmt.Errorf("unknown tasks sub-command %q (list|show|audit|cancel|resume|runs)", args[0])
	}
}

func runTasksList(args []string) error {
	fs := flag.NewFlagSet("tasks list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var adminAddr, adminToken, bootstrapPath string
	var source string
	var status string
	var limit int
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&source, "source", "", "filter by source metadata (best-effort from task meta.source)")
	fs.StringVar(&status, "status", "", "filter by status")
	fs.IntVar(&limit, "limit", 50, "max results")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	params := map[string]any{"limit": limit}
	if status != "" {
		params["status"] = status
	}
	result, err := cl.call("tasks.list", params)
	if err != nil {
		return fmt.Errorf("tasks.list: %w", err)
	}

	tasksRaw, ok := result["tasks"]
	if !ok || tasksRaw == nil {
		return fmt.Errorf("tasks.list: response missing tasks")
	}
	tasksList, err := decodeTaskSpecList(tasksRaw)
	if err != nil {
		return fmt.Errorf("tasks.list: decode tasks: %w", err)
	}
	if source != "" {
		tasksList = filterTasksBySource(tasksList, source)
	}
	if jsonOut {
		return printJSON(map[string]any{"tasks": tasksList, "count": len(tasksList)})
	}
	if len(tasksList) == 0 {
		fmt.Println("no tasks found")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tTITLE\tAPPROVAL\tVERIFICATION\tCREATED")
	for _, task := range tasksList {
		title := task.Title
		if len(title) > 40 {
			title = title[:37] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			task.TaskID,
			task.Status,
			title,
			approvalState(task),
			verificationState(task),
			formatUnixShort(task.CreatedAt),
		)
	}
	return tw.Flush()
}

func runTasksShow(args []string) error {
	fs := flag.NewFlagSet("tasks show", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
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
		return fmt.Errorf("usage: metiq tasks show <task-id>")
	}
	taskID := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("tasks.get", map[string]any{"task_id": taskID, "runs_limit": 100})
	if err != nil {
		return fmt.Errorf("tasks.get: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}

	taskRaw, ok := result["task"]
	if !ok || taskRaw == nil {
		return fmt.Errorf("tasks.get: response missing task")
	}
	task, err := decodeTaskSpec(taskRaw)
	if err != nil {
		return fmt.Errorf("tasks.get: decode task: %w", err)
	}
	runsRaw, ok := result["runs"]
	if !ok || runsRaw == nil {
		runsRaw = []any{}
	}
	runs, err := decodeTaskRunList(runsRaw)
	if err != nil {
		return fmt.Errorf("tasks.get: decode runs: %w", err)
	}
	fmt.Printf("Task: %s\n", task.TaskID)
	fmt.Printf("  Title:         %s\n", task.Title)
	fmt.Printf("  Status:        %s\n", task.Status)
	fmt.Printf("  Created:       %s\n", formatUnixRFC3339(task.CreatedAt))
	fmt.Printf("  Updated:       %s\n", formatUnixRFC3339(task.UpdatedAt))
	if task.AssignedAgent != "" {
		fmt.Printf("  Agent:         %s\n", task.AssignedAgent)
	}
	if task.Authority.AutonomyMode != "" {
		fmt.Printf("  Authority:     %s\n", task.Authority.AutonomyMode)
	}
	fmt.Printf("  Approval:      %s\n", approvalState(task))
	fmt.Printf("  Verification:  %s\n", verificationState(task))
	if !task.Budget.IsZero() {
		fmt.Printf("  Budget:        %s\n", budgetSummary(task.Budget))
	}

	fmt.Printf("\nRuns (%d):\n", len(runs))
	if len(runs) == 0 {
		fmt.Println("  (no runs)")
	} else {
		for i, run := range runs {
			fmt.Printf("  [%d] %s - %s\n", i+1, run.RunID, run.Status)
			if run.Error != "" {
				fmt.Printf("      Error: %s\n", run.Error)
			}
		}
	}
	return nil
}

func runTasksAudit(args []string) error {
	fs := flag.NewFlagSet("tasks audit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("tasks.summary", map[string]any{})
	if err != nil {
		return fmt.Errorf("tasks.summary: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}

	total, _ := result["total"].(float64)
	active, _ := result["active_count"].(float64)
	blocked, _ := result["blocked_count"].(float64)
	failed, _ := result["failed_count"].(float64)
	byStatus, _ := result["by_status"].(map[string]any)

	fmt.Println("Task Runtime Summary")
	fmt.Println(strings.Repeat("─", 40))
	fmt.Printf("Total Tasks:     %d\n", int(total))
	fmt.Printf("Active:          %d\n", int(active))
	fmt.Printf("Blocked:         %d\n", int(blocked))
	fmt.Printf("Failed:          %d\n", int(failed))
	fmt.Println()
	fmt.Println("By Status:")
	keys := make([]string, 0, len(byStatus))
	for key := range byStatus {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if count, ok := byStatus[key].(float64); ok {
			fmt.Printf("  %-18s %d\n", key+":", int(count))
		}
	}
	return nil
}

func runTasksCancel(args []string) error {
	fs := flag.NewFlagSet("tasks cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var adminAddr, adminToken, bootstrapPath string
	var reason string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&reason, "reason", "cancelled by user", "cancellation reason")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq tasks cancel <task-id>")
	}
	taskID := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	_, err = cl.call("tasks.cancel", map[string]any{"task_id": taskID, "reason": reason})
	if err != nil {
		return fmt.Errorf("tasks.cancel: %w", err)
	}
	fmt.Printf("task %s cancelled\n", taskID)
	return nil
}

func runTasksResume(args []string) error {
	fs := flag.NewFlagSet("tasks resume", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var adminAddr, adminToken, bootstrapPath string
	var decision string
	var reason string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&decision, "decision", "resume", "resume decision (resume|approved|rejected|amended)")
	fs.StringVar(&reason, "reason", "", "operator reason")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq tasks resume <task-id>")
	}
	taskID := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	params := map[string]any{"task_id": taskID, "decision": decision}
	if strings.TrimSpace(reason) != "" {
		params["reason"] = reason
	}
	result, err := cl.call("tasks.resume", params)
	if err != nil {
		return fmt.Errorf("tasks.resume: %w", err)
	}
	taskRaw, ok := result["task"]
	if !ok || taskRaw == nil {
		return fmt.Errorf("tasks.resume: response missing task")
	}
	task, err := decodeTaskSpec(taskRaw)
	if err != nil {
		return fmt.Errorf("tasks.resume: decode task: %w", err)
	}
	fmt.Printf("task %s resumed (%s) -> status=%s\n", taskID, decision, task.Status)
	return nil
}

func runTasksRuns(args []string) error {
	fs := flag.NewFlagSet("tasks runs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var adminAddr, adminToken, bootstrapPath string
	var taskID string
	var limit int
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&taskID, "task", "", "filter by task ID")
	fs.IntVar(&limit, "limit", 50, "max results")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	runs := make([]state.TaskRun, 0)
	if strings.TrimSpace(taskID) != "" {
		getResult, err := cl.call("tasks.get", map[string]any{"task_id": taskID, "runs_limit": limit})
		if err != nil {
			return fmt.Errorf("tasks.get(%s): %w", taskID, err)
		}
		runsRaw, ok := getResult["runs"]
		if !ok || runsRaw == nil {
			runsRaw = []any{}
		}
		runs, err = decodeTaskRunList(runsRaw)
		if err != nil {
			return fmt.Errorf("tasks.get(%s): decode runs: %w", taskID, err)
		}
	} else {
		tasksResult, err := cl.call("tasks.list", map[string]any{"limit": limit})
		if err != nil {
			return fmt.Errorf("tasks.list: %w", err)
		}
		tasksRaw, ok := tasksResult["tasks"]
		if !ok || tasksRaw == nil {
			return fmt.Errorf("tasks.list: response missing tasks")
		}
		tasksList, err := decodeTaskSpecList(tasksRaw)
		if err != nil {
			return fmt.Errorf("tasks.list: decode tasks: %w", err)
		}
		for _, task := range tasksList {
			getResult, err := cl.call("tasks.get", map[string]any{"task_id": task.TaskID, "runs_limit": limit})
			if err != nil {
				return fmt.Errorf("tasks.get(%s): %w", task.TaskID, err)
			}
			runsRaw, ok := getResult["runs"]
			if !ok || runsRaw == nil {
				runsRaw = []any{}
			}
			decoded, err := decodeTaskRunList(runsRaw)
			if err != nil {
				return fmt.Errorf("tasks.get(%s): decode runs: %w", task.TaskID, err)
			}
			runs = append(runs, decoded...)
		}
	}
	if len(runs) > limit {
		runs = runs[:limit]
	}

	if jsonOut {
		return printJSON(map[string]any{"runs": runs})
	}
	if len(runs) == 0 {
		fmt.Println("no runs found")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN_ID\tTASK_ID\tSTATUS\tSTARTED\tDURATION")
	for _, r := range runs {
		startedStr := "-"
		durationStr := "-"
		if r.StartedAt > 0 {
			startedStr = time.Unix(r.StartedAt, 0).Format("2006-01-02 15:04")
			if r.EndedAt > 0 {
				duration := time.Unix(r.EndedAt, 0).Sub(time.Unix(r.StartedAt, 0))
				durationStr = duration.String()
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.RunID, r.TaskID, r.Status, startedStr, durationStr)
	}
	return tw.Flush()
}

func decodeTaskSpec(raw any) (state.TaskSpec, error) {
	if raw == nil {
		return state.TaskSpec{}, fmt.Errorf("task payload is nil")
	}
	var task state.TaskSpec
	b, err := json.Marshal(raw)
	if err != nil {
		return state.TaskSpec{}, fmt.Errorf("marshal task payload: %w", err)
	}
	if err := json.Unmarshal(b, &task); err != nil {
		return state.TaskSpec{}, fmt.Errorf("unmarshal task payload: %w", err)
	}
	return task, nil
}

func decodeTaskSpecList(raw any) ([]state.TaskSpec, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("tasks payload must be an array, got %T", raw)
	}
	out := make([]state.TaskSpec, 0, len(items))
	for i, item := range items {
		task, err := decodeTaskSpec(item)
		if err != nil {
			return nil, fmt.Errorf("task[%d]: %w", i, err)
		}
		out = append(out, task)
	}
	return out, nil
}

func decodeTaskRunList(raw any) ([]state.TaskRun, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("runs payload must be an array, got %T", raw)
	}
	out := make([]state.TaskRun, 0, len(items))
	for i, item := range items {
		var run state.TaskRun
		b, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("marshal run[%d]: %w", i, err)
		}
		if err := json.Unmarshal(b, &run); err != nil {
			return nil, fmt.Errorf("unmarshal run[%d]: %w", i, err)
		}
		out = append(out, run)
	}
	return out, nil
}

func formatUnixShort(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return time.Unix(ts, 0).Format("2006-01-02 15:04")
}

func formatUnixRFC3339(ts int64) string {
	if ts <= 0 {
		return "-"
	}
	return time.Unix(ts, 0).Format(time.RFC3339)
}

func approvalState(task state.TaskSpec) string {
	if strings.TrimSpace(string(task.Status)) == string(state.TaskStatusAwaitingApproval) {
		return "awaiting_approval"
	}
	if task.Meta != nil {
		if decision, ok := task.Meta["approval_decision"].(string); ok && strings.TrimSpace(decision) != "" {
			return decision
		}
	}
	return "-"
}

func verificationState(task state.TaskSpec) string {
	if task.Meta != nil {
		if status, ok := task.Meta["verification_status"].(string); ok && strings.TrimSpace(status) != "" {
			return status
		}
	}
	if len(task.Verification.Checks) > 0 {
		return "configured"
	}
	return "-"
}

func budgetSummary(b state.TaskBudget) string {
	parts := make([]string, 0, 3)
	if b.MaxTotalTokens > 0 {
		parts = append(parts, fmt.Sprintf("tokens<=%d", b.MaxTotalTokens))
	}
	if b.MaxRuntimeMS > 0 {
		parts = append(parts, fmt.Sprintf("runtime<=%dms", b.MaxRuntimeMS))
	}
	if b.MaxToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("tools<=%d", b.MaxToolCalls))
	}
	if len(parts) == 0 {
		return "set"
	}
	return strings.Join(parts, ", ")
}

func filterTasksBySource(tasksList []state.TaskSpec, source string) []state.TaskSpec {
	source = strings.TrimSpace(strings.ToLower(source))
	if source == "" {
		return tasksList
	}
	filtered := make([]state.TaskSpec, 0, len(tasksList))
	for _, task := range tasksList {
		if task.Meta == nil {
			continue
		}
		raw, _ := task.Meta["source"].(string)
		if strings.ToLower(strings.TrimSpace(raw)) == source {
			filtered = append(filtered, task)
		}
	}
	return filtered
}
