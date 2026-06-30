package main

import (
	"flag"
	"fmt"
)

func runACP(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("acp subcommands: dispatch, pipeline, status")
	}
	switch args[0] {
	case "dispatch":
		return runACPDispatch(args[1:])
	case "pipeline":
		return runACPPipeline(args[1:])
	case "status":
		return runACPStatus(args[1:])
	default:
		return fmt.Errorf("unknown acp sub-command %q (dispatch|pipeline|status)", args[0])
	}
}

func runACPDispatch(args []string) error {
	fs := flag.NewFlagSet("acp dispatch", flag.ContinueOnError)
	var flags metiqGatewayFlags
	var targetPubKey, instructions string
	addMetiqGatewayFlags(fs, &flags)
	fs.StringVar(&targetPubKey, "target-pubkey", "", "target ACP peer pubkey")
	fs.StringVar(&instructions, "instructions", "", "task instructions")
	if err := fs.Parse(args); err != nil {
		return err
	}
	params, err := metiqJSONOrKeyValueParams(fs.Args())
	if err != nil {
		return err
	}
	if targetPubKey != "" {
		params["target_pubkey"] = targetPubKey
	}
	if instructions != "" {
		params["instructions"] = instructions
	}
	if params["target_pubkey"] == nil || params["instructions"] == nil {
		return fmt.Errorf("usage: metiq acp dispatch --target-pubkey <pubkey> --instructions <text>")
	}
	result, err := metiqAdminCall(flags, "acp.dispatch", params)
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}

func runACPPipeline(args []string) error {
	fs := flag.NewFlagSet("acp pipeline", flag.ContinueOnError)
	var flags metiqGatewayFlags
	addMetiqGatewayFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return err
	}
	params, err := metiqJSONOrKeyValueParams(fs.Args())
	if err != nil {
		return err
	}
	if len(params) == 0 {
		return fmt.Errorf("usage: metiq acp pipeline '{\"steps\":[...]}'")
	}
	result, err := metiqAdminCall(flags, "acp.pipeline", params)
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}

func runACPStatus(args []string) error {
	fs := flag.NewFlagSet("acp status", flag.ContinueOnError)
	var flags metiqGatewayFlags
	addMetiqGatewayFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, err := metiqAdminCall(flags, "acp.manager.status", map[string]any{})
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}
