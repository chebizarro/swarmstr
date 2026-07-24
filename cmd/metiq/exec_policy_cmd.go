package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"

	"metiq/internal/permissions"
)

func runExecPolicy(args []string) error {
	if len(args) == 0 {
		return runExecPolicyGet(nil)
	}
	switch args[0] {
	case "get", "show", "list", "ls":
		return runExecPolicyGet(args[1:])
	case "set":
		return runExecPolicySet(args[1:])
	case "add", "require":
		return runExecPolicyMutateTools(args[1:], true)
	case "remove", "rm", "allow":
		return runExecPolicyMutateTools(args[1:], false)
	case "doctor", "lint":
		return runExecPolicyDoctor(args[1:])
	default:
		return fmt.Errorf("exec-policy subcommands: get, set, add, remove, doctor")
	}
}

func runExecPolicyDoctor(args []string) error {
	fs := flag.NewFlagSet("exec-policy doctor", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, nodeID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&nodeID, "node", "", "node ID for node-specific approvals")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	method := "exec.approvals.get"
	params := map[string]any{}
	if strings.TrimSpace(nodeID) != "" {
		method = "exec.approvals.node.get"
		params["node_id"] = strings.TrimSpace(nodeID)
	}
	result, err := cl.call(method, params)
	if err != nil {
		return err
	}
	policy, _ := result["approvals"].(map[string]any)
	report := permissions.DoctorExecApprovalPolicy(policy)
	if jsonOut {
		if err := printJSON(map[string]any{"valid": report.Valid(), "findings": report.Findings}); err != nil {
			return err
		}
	} else if len(report.Findings) == 0 {
		printSuccess("exec approval policy passed all doctor checks")
	} else {
		for _, finding := range report.Findings {
			fmt.Printf("%s\t%s\t%s\t%s\n", finding.Severity, finding.Code, finding.Field, finding.Message)
		}
	}
	if !report.Valid() {
		return fmt.Errorf("exec approval doctor found invalid policy")
	}
	return nil
}

func runExecPolicyGet(args []string) error {
	fs := flag.NewFlagSet("exec-policy get", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, nodeID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&nodeID, "node", "", "node ID for node-specific approvals")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	method := "exec.approvals.get"
	params := map[string]any{}
	if strings.TrimSpace(nodeID) != "" {
		method = "exec.approvals.node.get"
		params["node_id"] = strings.TrimSpace(nodeID)
	}
	result, err := cl.call(method, params)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	approvals, _ := result["approvals"].(map[string]any)
	printExecPolicyTable(approvals)
	return nil
}

func runExecPolicySet(args []string) error {
	fs := flag.NewFlagSet("exec-policy set", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, nodeID, mode string
	var timeoutMS int
	var jsonOut bool
	var tools csvListFlag
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&nodeID, "node", "", "node ID for node-specific approvals")
	fs.StringVar(&mode, "mode", "ask", "policy mode label: ask, allow, or deny")
	fs.IntVar(&timeoutMS, "timeout-ms", 0, "approval timeout in milliseconds")
	fs.Var(&tools, "tool", "tool requiring approval (repeatable or comma-separated)")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	policy := map[string]any{"mode": strings.TrimSpace(mode)}
	if len(tools) > 0 {
		policy["tools"] = []string(tools)
	}
	if timeoutMS > 0 {
		policy["timeout_ms"] = timeoutMS
	}
	return execPolicySet(adminAddr, adminToken, bootstrapPath, nodeID, policy, jsonOut)
}

func runExecPolicyMutateTools(args []string, add bool) error {
	fs := flag.NewFlagSet("exec-policy mutate", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, nodeID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.StringVar(&nodeID, "node", "", "node ID for node-specific approvals")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq exec-policy %s <tool> [tool...]", map[bool]string{true: "add", false: "remove"}[add])
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	method := "exec.approvals.get"
	params := map[string]any{}
	if strings.TrimSpace(nodeID) != "" {
		method = "exec.approvals.node.get"
		params["node_id"] = strings.TrimSpace(nodeID)
	}
	current, err := cl.call(method, params)
	if err != nil {
		return err
	}
	policy, _ := current["approvals"].(map[string]any)
	if policy == nil {
		policy = map[string]any{}
	}
	set := map[string]bool{}
	for _, tool := range stringSliceAny(policy["tools"]) {
		tool = strings.TrimSpace(tool)
		if tool != "" {
			set[tool] = true
		}
	}
	for _, tool := range fs.Args() {
		tool = strings.TrimSpace(tool)
		if tool == "" {
			continue
		}
		if add {
			set[tool] = true
		} else {
			delete(set, tool)
		}
	}
	tools := make([]string, 0, len(set))
	for tool := range set {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	policy["tools"] = tools
	return execPolicySet(adminAddr, adminToken, bootstrapPath, nodeID, policy, jsonOut)
}

func execPolicySet(adminAddr, adminToken, bootstrapPath, nodeID string, policy map[string]any, jsonOut bool) error {
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	method := "exec.approvals.set"
	params := map[string]any{"approvals": policy}
	if strings.TrimSpace(nodeID) != "" {
		method = "exec.approvals.node.set"
		params["node_id"] = strings.TrimSpace(nodeID)
	}
	result, err := cl.call(method, params)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	printSuccess("exec approval policy updated")
	if approvals, _ := result["approvals"].(map[string]any); approvals != nil {
		printExecPolicyTable(approvals)
	}
	return nil
}

func printExecPolicyTable(policy map[string]any) {
	if len(policy) == 0 {
		fmt.Println("no exec approval policy configured")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tVALUE")
	keys := make([]string, 0, len(policy))
	for key := range policy {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(w, "%s\t%v\n", key, policy[key])
	}
	_ = w.Flush()
}
