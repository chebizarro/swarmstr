package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

// ─── approvals ────────────────────────────────────────────────────────────────

func runApprovals(args []string) error {
	if len(args) == 0 {
		return runApprovalsList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runApprovalsList(args[1:])
	case "approve":
		return runApprovalsResolve(args[1:], "approved")
	case "deny", "reject":
		return runApprovalsResolve(args[1:], "denied")
	default:
		return fmt.Errorf("unknown approvals sub-command %q (list|approve|deny)", args[0])
	}
}

func runApprovalsList(args []string) error {
	fs := flag.NewFlagSet("approvals list", flag.ContinueOnError)
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
	result, err := cl.call("exec.approvals.get", map[string]any{})
	if err != nil {
		return fmt.Errorf("exec.approvals.get: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}
	pending, _ := result["pending"].([]any)
	if len(pending) == 0 {
		fmt.Println("no pending approvals")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSESSION\tCOMMAND\tCREATED")
	for _, p := range pending {
		pm, _ := p.(map[string]any)
		id := stringFieldAny(pm, "id")
		sess := stringFieldAny(pm, "session_id")
		cmd := stringFieldAny(pm, "command")
		created := floatFieldAny(pm, "created_at")
		createdStr := "-"
		if created > 0 {
			createdStr = time.Unix(int64(created), 0).Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", id, sess, cmd, createdStr)
	}
	return tw.Flush()
}

func runApprovalsResolve(args []string, decision string) error {
	fs := flag.NewFlagSet("approvals "+decision, flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) == 0 {
		return fmt.Errorf("usage: metiq approvals %s <approval-id>", decision)
	}
	approvalID := fs.Arg(0)
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("exec.approval.resolve", map[string]any{
		"id":       approvalID,
		"decision": decision,
	})
	if err != nil {
		return fmt.Errorf("exec.approval.resolve: %w", err)
	}
	return printJSON(result)
}
