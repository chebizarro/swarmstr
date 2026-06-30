package main

import (
	"flag"
	"fmt"
)

func runCommitments(args []string) error {
	if len(args) == 0 {
		return runCommitmentsList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runCommitmentsList(args[1:])
	case "add":
		return runCommitmentsAdd(args[1:])
	case "status":
		return runCommitmentsStatus(args[1:])
	default:
		return fmt.Errorf("unknown commitments sub-command %q (list|add|status)", args[0])
	}
}

func runCommitmentsList(args []string) error {
	fs := flag.NewFlagSet("commitments list", flag.ContinueOnError)
	var flags metiqGatewayFlags
	var agentID, status string
	var all bool
	addMetiqGatewayFlags(fs, &flags)
	fs.StringVar(&agentID, "agent", "", "filter by agent id")
	fs.StringVar(&status, "status", "", "filter by commitment status")
	fs.BoolVar(&all, "all", false, "show all statuses")
	if err := fs.Parse(args); err != nil {
		return err
	}
	params := map[string]any{"all": all}
	if agentID != "" {
		params["agent_id"] = agentID
	}
	if status != "" {
		params["status"] = status
	}
	result, err := metiqAdminCall(flags, "commitments.list", params)
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}

func runCommitmentsAdd(args []string) error {
	fs := flag.NewFlagSet("commitments add", flag.ContinueOnError)
	var flags metiqGatewayFlags
	var agentID, text, dueAt string
	addMetiqGatewayFlags(fs, &flags)
	fs.StringVar(&agentID, "agent", "", "agent id")
	fs.StringVar(&text, "text", "", "commitment text")
	fs.StringVar(&dueAt, "due-at", "", "optional due time")
	if err := fs.Parse(args); err != nil {
		return err
	}
	params, err := metiqJSONOrKeyValueParams(fs.Args())
	if err != nil {
		return err
	}
	if agentID != "" {
		params["agent_id"] = agentID
	}
	if text != "" {
		params["text"] = text
	}
	if dueAt != "" {
		params["due_at"] = dueAt
	}
	if params["text"] == nil {
		return fmt.Errorf("usage: metiq commitments add --text <text> [--agent <id>] [--due-at <time>]")
	}
	result, err := metiqAdminCall(flags, "commitments.add", params)
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}

func runCommitmentsStatus(args []string) error {
	fs := flag.NewFlagSet("commitments status", flag.ContinueOnError)
	var flags metiqGatewayFlags
	addMetiqGatewayFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, err := metiqAdminCall(flags, "commitments.status", map[string]any{})
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}
