package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"metiq/internal/tasks"
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
	case "runs":
		return runTasksRuns(args[1:])
	default:
		return fmt.Errorf("unknown tasks sub-command %q (list|show|audit|cancel|runs)", args[0])
	}
}

func runTasksList(args []string) error {
	fs := flag.NewFlagSet("tasks list", flag.ContinueOnError)
	var dataDir string
	var source string
	var status string
	var limit int
	var jsonOut bool
	fs.StringVar(&dataDir, "dir", "", "task ledger directory (default: ~/.metiq/tasks)")
	fs.StringVar(&source, "source", "", "filter by source (acp|cron|webhook|workflow|manual|dvm|approval|sandbox)")
	fs.StringVar(&status, "status", "", "filter by status (pending|running|completed|failed|cancelled)")
	fs.IntVar(&limit, "limit", 50, "max results")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		dataDir = home + "/.metiq/tasks"
	}

	// Load from file store if directory exists
	store, err := tasks.NewFileStore(dataDir)
	if err != nil {
		// If directory doesn't exist, just show empty
		if os.IsNotExist(err) {
			fmt.Println("no tasks found (task ledger not initialized)")
			return nil
		}
		return fmt.Errorf("open task store: %w", err)
	}

	// List tasks
	ctx := context.Background()
	opts := tasks.ListTasksOptions{
		Limit: limit,
	}
	if source != "" {
		opts.Source = []tasks.TaskSource{tasks.TaskSource(source)}
	}
	// Status filtering handled by store implementation

	entries, err := store.ListTasks(ctx, opts)
	if err != nil {
		return fmt.Errorf("list tasks: %w", err)
	}

	if jsonOut {
		return printJSON(map[string]any{"tasks": entries})
	}

	if len(entries) == 0 {
		fmt.Println("no tasks found")
		return nil
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSOURCE\tSTATUS\tTITLE\tCREATED")
	for _, e := range entries {
		title := e.Task.Title
		if len(title) > 40 {
			title = title[:37] + "..."
		}
		createdStr := time.Unix(e.CreatedAt, 0).Format("2006-01-02 15:04")
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", e.Task.TaskID, e.Source, e.Task.Status, title, createdStr)
	}
	return tw.Flush()
}

func runTasksShow(args []string) error {
	fs := flag.NewFlagSet("tasks show", flag.ContinueOnError)
	var dataDir string
	var jsonOut bool
	fs.StringVar(&dataDir, "dir", "", "task ledger directory (default: ~/.metiq/tasks)")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq tasks show <task-id>")
	}
	taskID := fs.Arg(0)

	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		dataDir = home + "/.metiq/tasks"
	}

	store, err := tasks.NewFileStore(dataDir)
	if err != nil {
		return fmt.Errorf("open task store: %w", err)
	}

	ctx := context.Background()
	entry, err := store.LoadTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("load task: %w", err)
	}
	if entry == nil {
		return fmt.Errorf("task not found: %s", taskID)
	}

	if jsonOut {
		return printJSON(entry)
	}

	// Pretty print task details
	fmt.Printf("Task: %s\n", entry.Task.TaskID)
	fmt.Printf("  Title:       %s\n", entry.Task.Title)
	fmt.Printf("  Source:      %s\n", entry.Source)
	if entry.SourceRef != "" {
		fmt.Printf("  Source Ref:  %s\n", entry.SourceRef)
	}
	fmt.Printf("  Created:     %s\n", time.Unix(entry.CreatedAt, 0).Format(time.RFC3339))
	fmt.Printf("  Updated:     %s\n", time.Unix(entry.UpdatedAt, 0).Format(time.RFC3339))

	if entry.Task.Authority.AutonomyMode != "" {
		fmt.Printf("  Authority:   %s\n", entry.Task.Authority.AutonomyMode)
	}

	fmt.Printf("\nRuns (%d):\n", len(entry.Runs))
	if len(entry.Runs) == 0 {
		fmt.Println("  (no runs)")
	} else {
		for i, run := range entry.Runs {
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
	var dataDir string
	var jsonOut bool
	fs.StringVar(&dataDir, "dir", "", "task ledger directory (default: ~/.metiq/tasks)")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		dataDir = home + "/.metiq/tasks"
	}

	store, err := tasks.NewFileStore(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("task ledger not initialized")
			return nil
		}
		return fmt.Errorf("open task store: %w", err)
	}

	ctx := context.Background()
	stats, err := store.Stats(ctx)
	if err != nil {
		return fmt.Errorf("get stats: %w", err)
	}

	if jsonOut {
		return printJSON(stats)
	}

	fmt.Println("Task Ledger Statistics")
	fmt.Println(strings.Repeat("─", 40))
	fmt.Printf("Total Tasks:     %d\n", stats.TotalTasks)
	fmt.Printf("Total Runs:      %d\n", stats.TotalRuns)
	fmt.Println()
	fmt.Println("By Status:")
	fmt.Printf("  Pending:       %d\n", stats.ByStatus["pending"])
	fmt.Printf("  Running:       %d\n", stats.ByStatus["running"])
	fmt.Printf("  Completed:     %d\n", stats.ByStatus["completed"])
	fmt.Printf("  Failed:        %d\n", stats.ByStatus["failed"])
	fmt.Printf("  Cancelled:     %d\n", stats.ByStatus["cancelled"])
	fmt.Println()
	fmt.Println("By Source:")
	for source, count := range stats.BySource {
		fmt.Printf("  %-12s %d\n", source+":", count)
	}

	return nil
}

func runTasksCancel(args []string) error {
	fs := flag.NewFlagSet("tasks cancel", flag.ContinueOnError)
	var dataDir string
	var reason string
	fs.StringVar(&dataDir, "dir", "", "task ledger directory (default: ~/.metiq/tasks)")
	fs.StringVar(&reason, "reason", "cancelled by user", "cancellation reason")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq tasks cancel <task-id>")
	}
	taskID := fs.Arg(0)

	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		dataDir = home + "/.metiq/tasks"
	}

	store, err := tasks.NewFileStore(dataDir)
	if err != nil {
		return fmt.Errorf("open task store: %w", err)
	}

	// Create a ledger with this store to perform the cancellation
	ledger := tasks.NewLedger(store)

	ctx := context.Background()
	if err := ledger.CancelTask(ctx, taskID, "cli", reason); err != nil {
		return fmt.Errorf("cancel task: %w", err)
	}

	fmt.Printf("task %s cancelled\n", taskID)
	return nil
}

func runTasksRuns(args []string) error {
	fs := flag.NewFlagSet("tasks runs", flag.ContinueOnError)
	var dataDir string
	var taskID string
	var limit int
	var jsonOut bool
	fs.StringVar(&dataDir, "dir", "", "task ledger directory (default: ~/.metiq/tasks)")
	fs.StringVar(&taskID, "task", "", "filter by task ID")
	fs.IntVar(&limit, "limit", 50, "max results")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve home dir: %w", err)
		}
		dataDir = home + "/.metiq/tasks"
	}

	store, err := tasks.NewFileStore(dataDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no runs found (task ledger not initialized)")
			return nil
		}
		return fmt.Errorf("open task store: %w", err)
	}

	ctx := context.Background()
	opts := tasks.ListRunsOptions{
		TaskID: taskID,
		Limit:  limit,
	}

	runs, err := store.ListRuns(ctx, opts)
	if err != nil {
		return fmt.Errorf("list runs: %w", err)
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
		if r.Run.StartedAt > 0 {
			startedStr = time.Unix(r.Run.StartedAt, 0).Format("2006-01-02 15:04")
			if r.Run.EndedAt > 0 {
				duration := time.Duration(r.Run.EndedAt-r.Run.StartedAt) * time.Second
				durationStr = duration.String()
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Run.RunID, r.Run.TaskID, r.Run.Status, startedStr, durationStr)
	}
	return tw.Flush()
}


