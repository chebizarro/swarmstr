package main

import (
	"flag"
	"fmt"
	"os"

	qa "metiq/testing/qa"
)

func runQA(args []string) error {
	if len(args) == 0 {
		return runQARun(nil)
	}
	switch args[0] {
	case "run":
		return runQARun(args[1:])
	default:
		return fmt.Errorf("qa subcommands: run")
	}
}

func runQARun(args []string) error {
	fs := flag.NewFlagSet("qa run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var dir, repo string
	var jsonOut bool
	fs.StringVar(&dir, "dir", "qa/scenarios", "scenario root")
	fs.StringVar(&repo, "repo", ".", "repository root for deterministic checks")
	fs.BoolVar(&jsonOut, "json", true, "emit JSON report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := qa.Run(dir, repo)
	if err != nil {
		return err
	}
	if jsonOut {
		raw, _ := report.JSON()
		fmt.Println(string(raw))
	} else {
		fmt.Printf("qa scenarios: %d passed, %d failed\n", report.Passed, report.Failed)
	}
	if report.Failed > 0 {
		return fmt.Errorf("%d scenario(s) failed", report.Failed)
	}
	return nil
}
