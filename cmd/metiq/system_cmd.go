package main

import (
	"flag"
	"fmt"
)

func runSystem(args []string) error {
	if len(args) == 0 {
		return runSystemStatus(args)
	}
	switch args[0] {
	case "status":
		return runSystemStatus(args[1:])
	case "info":
		return runSystemInfo(args[1:])
	default:
		return fmt.Errorf("unknown system sub-command %q (status|info)", args[0])
	}
}

func runSystemStatus(args []string) error {
	fs := flag.NewFlagSet("system status", flag.ContinueOnError)
	var flags metiqGatewayFlags
	addMetiqGatewayFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return err
	}
	status, err := metiqAdminCall(flags, "status.get", map[string]any{})
	if err != nil {
		return err
	}
	health, err := metiqAdminCall(flags, "health", map[string]any{})
	if err != nil {
		return err
	}
	return printMetiqCallResult(map[string]any{"status": status, "health": health})
}

func runSystemInfo(args []string) error {
	fs := flag.NewFlagSet("system info", flag.ContinueOnError)
	var flags metiqGatewayFlags
	addMetiqGatewayFlags(fs, &flags)
	if err := fs.Parse(args); err != nil {
		return err
	}
	methods, err := metiqAdminCall(flags, "supportedmethods", map[string]any{})
	if err != nil {
		return err
	}
	status, err := metiqAdminCall(flags, "status.get", map[string]any{})
	if err != nil {
		return err
	}
	return printMetiqCallResult(map[string]any{"methods": methods, "status": status})
}
