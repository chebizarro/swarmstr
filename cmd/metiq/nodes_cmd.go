package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// ─── nodes ────────────────────────────────────────────────────────────────────

// runNodesList lists known remote metiq nodes via the daemon's node.list method.
func runNodesList(args []string) error {
	fs := flag.NewFlagSet("nodes list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.list", map[string]any{})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	nodes, _ := result["nodes"].([]any)
	if len(nodes) == 0 {
		fmt.Println("No remote nodes registered.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NODE ID\tNAME\tSTATUS")
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		id := stringFieldAny(node, "node_id")
		name := stringFieldAny(node, "name")
		status := stringFieldAny(node, "status")
		if status == "" {
			status = "unknown"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, name, status)
	}
	return w.Flush()
}

// runNodesAdd adds a remote node by Nostr pubkey (hex or npub).
func runNodesAdd(args []string) error {
	fs := flag.NewFlagSet("nodes add", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, name string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&name, "name", "", "human-readable name for this node")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes add <npub|hex-pubkey> [--name <label>]")
	}
	pubkey := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	params := map[string]any{"node_id": pubkey}
	if name != "" {
		params["name"] = name
	}
	result, err := cl.call("node.describe", params)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Note: node %q not yet known to daemon (%v).\n", pubkey, err)
		fmt.Fprintf(os.Stderr, "Add it to the 'nodes' section of your config file and restart.\n")
		return nil
	}
	return printJSON(result)
}

// runNodesStatus pings a remote node and reports its status.
func runNodesStatus(args []string) error {
	fs := flag.NewFlagSet("nodes status", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes status <npub|hex-pubkey>")
	}
	nodeID := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("node.describe", map[string]any{"node_id": nodeID})
	if err != nil {
		return fmt.Errorf("node.describe: %w", err)
	}
	if jsonOut {
		return printJSON(result)
	}
	statusStr := stringFieldAny(result, "status")
	name := stringFieldAny(result, "name")
	fmt.Printf("Node:   %s\n", nodeID)
	if name != "" {
		fmt.Printf("Name:   %s\n", name)
	}
	fmt.Printf("Status: %s\n", statusStr)
	return nil
}

// runNodesSend sends a DM to a remote metiq node.
func runNodesSend(args []string) error {
	fs := flag.NewFlagSet("nodes send", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: metiq nodes send <npub|hex-pubkey> <message>")
	}
	to := fs.Arg(0)
	message := strings.Join(fs.Args()[1:], " ")

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("chat.send", map[string]any{"to": to, "message": message})
	if err != nil {
		return fmt.Errorf("chat.send: %w", err)
	}
	return printJSON(result)
}

// runNodes dispatches nodes sub-commands.
func runNodes(args []string) error {
	if len(args) == 0 {
		return runNodesList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runNodesList(args[1:])
	case "add":
		return runNodesAdd(args[1:])
	case "status":
		return runNodesStatus(args[1:])
	case "send":
		return runNodesSend(args[1:])
	case "pending":
		return runNodesPending(args[1:])
	case "approve":
		return runNodesApprove(args[1:])
	case "reject":
		return runNodesReject(args[1:])
	case "describe":
		return runNodesDescribe(args[1:])
	case "invoke":
		return runNodesInvoke(args[1:])
	case "rename":
		return runNodesRename(args[1:])
	default:
		return fmt.Errorf("unknown nodes sub-command %q (list|add|status|send|pending|approve|reject|describe|invoke|rename)", args[0])
	}
}

// runNodesPending lists pending node pairing requests.
func runNodesPending(args []string) error {
	fs := flag.NewFlagSet("nodes pending", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut, includePaired bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	fs.BoolVar(&includePaired, "include-paired", false, "also print currently paired nodes")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.pair.list", map[string]any{})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	pending, _ := result["pending"].([]any)
	paired, _ := result["paired"].([]any)
	if len(pending) == 0 && (!includePaired || len(paired) == 0) {
		fmt.Println("No pending node pairing requests.")
		return nil
	}
	if len(pending) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "REQUEST ID\tNODE ID\tSTATUS")
		for _, r := range pending {
			req, ok := r.(map[string]any)
			if !ok {
				continue
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n",
				stringFieldAny(req, "request_id"),
				stringFieldAny(req, "node_id"),
				stringFieldAny(req, "status"),
			)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	if includePaired {
		if len(paired) > 0 {
			if len(pending) > 0 {
				fmt.Println()
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "PAIRED NODE ID\tDISPLAY NAME\tAPPROVED AT")
			for _, r := range paired {
				node, ok := r.(map[string]any)
				if !ok {
					continue
				}
				fmt.Fprintf(w, "%s\t%s\t%d\n",
					stringFieldAny(node, "node_id"),
					stringFieldAny(node, "display_name"),
					int64(floatFieldAny(node, "approved_at_ms")),
				)
			}
			if err := w.Flush(); err != nil {
				return err
			}
		} else if len(pending) == 0 {
			fmt.Println("No paired nodes.")
		}
	}
	return nil
}

// runNodesApprove approves a pending node pairing request.
func runNodesApprove(args []string) error {
	fs := flag.NewFlagSet("nodes approve", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes approve <request-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.pair.approve", map[string]any{"request_id": fs.Arg(0)})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// runNodesReject rejects a pending node pairing request.
func runNodesReject(args []string) error {
	fs := flag.NewFlagSet("nodes reject", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes reject <request-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.pair.reject", map[string]any{"request_id": fs.Arg(0)})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// runNodesDescribe shows detailed info about a node.
func runNodesDescribe(args []string) error {
	fs := flag.NewFlagSet("nodes describe", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq nodes describe <node-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.describe", map[string]any{"node_id": fs.Arg(0)})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// runNodesInvoke invokes a command on a remote node.
func runNodesInvoke(args []string) error {
	fs := flag.NewFlagSet("nodes invoke", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, nodeID, command, rawArgs string
	var timeoutSeconds int
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&nodeID, "node", "", "node ID (required)")
	fs.StringVar(&command, "command", "", "command to invoke (required)")
	fs.StringVar(&rawArgs, "args", "", "JSON args to pass to the command")
	fs.IntVar(&timeoutSeconds, "timeout", 30, "timeout in seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if nodeID == "" || command == "" {
		return fmt.Errorf("usage: metiq nodes invoke --node <id> --command <cmd> [--args '{...}']")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	body := map[string]any{
		"node_id":    nodeID,
		"command":    command,
		"timeout_ms": timeoutSeconds * 1000,
	}
	if rawArgs != "" {
		var argsMap map[string]any
		if err := json.Unmarshal([]byte(rawArgs), &argsMap); err != nil {
			return fmt.Errorf("invalid --args JSON: %w", err)
		}
		body["args"] = argsMap
	}
	result, err := cl.call("node.invoke", body)
	if err != nil {
		return err
	}
	return printJSON(result)
}

// runNodesRename renames a remote node.
func runNodesRename(args []string) error {
	fs := flag.NewFlagSet("nodes rename", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 2 {
		return fmt.Errorf("usage: metiq nodes rename <node-id> <new-name>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("node.rename", map[string]any{
		"node_id": fs.Arg(0),
		"name":    fs.Arg(1),
	})
	if err != nil {
		return err
	}
	return printJSON(result)
}

// stringFieldAny is like stringField but operates on map[string]any.
