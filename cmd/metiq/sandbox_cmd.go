package main

import (
	"flag"
	"fmt"
)

func runSandbox(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("sandbox subcommands: run, status")
	}
	switch args[0] {
	case "run":
		return runSandboxRun(args[1:])
	case "status":
		return runSandboxStatus(args[1:])
	default:
		return fmt.Errorf("unknown sandbox sub-command %q (run|status)", args[0])
	}
}

func runSandboxRun(args []string) error {
	fs := flag.NewFlagSet("sandbox run", flag.ContinueOnError)
	var flags metiqGatewayFlags
	addMetiqGatewayFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq sandbox run <command> [args...]")
	}
	result, err := metiqAdminCall(flags, "sandbox.run", map[string]any{"cmd": fs.Args()})
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}

func runSandboxStatus(args []string) error {
	fs := flag.NewFlagSet("sandbox status", flag.ContinueOnError)
	var flags metiqGatewayFlags
	addMetiqGatewayFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, err := metiqAdminCall(flags, "sandbox.run", map[string]any{"cmd": []string{"true"}, "dry_run": true})
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}
