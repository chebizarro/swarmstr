package main

import (
	"flag"
	"fmt"
	"os"

	"metiq/internal/trajectory"
)

func runTrajectory(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("trajectory subcommands: export, list, cleanup")
	}
	switch args[0] {
	case "export":
		return runTrajectoryExport(args[1:])
	case "list":
		return runTrajectoryList(args[1:])
	case "cleanup":
		return runTrajectoryCleanup(args[1:])
	default:
		return fmt.Errorf("unknown trajectory subcommand %q (export|list|cleanup)", args[0])
	}
}

func runTrajectoryExport(args []string) error {
	fs := flag.NewFlagSet("trajectory export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var root, out string
	var audit bool
	fs.StringVar(&root, "dir", "", "trajectory root (default ~/.metiq/trajectories)")
	fs.StringVar(&out, "out", "", "output support-bundle zip path")
	fs.BoolVar(&audit, "audit-event", false, "print unsigned Nostr audit summary event JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq trajectory export <session-id> [--out bundle.zip]")
	}
	manifest, err := trajectory.ExportBundle(root, fs.Arg(0), out)
	if err != nil {
		return err
	}
	if audit {
		ev, err := trajectory.BuildNostrAuditSummary(manifest.Summary)
		if err != nil {
			return err
		}
		return printJSON(ev)
	}
	return printJSON(manifest)
}

func runTrajectoryList(args []string) error {
	fs := flag.NewFlagSet("trajectory list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var root string
	fs.StringVar(&root, "dir", "", "trajectory root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	sessions, err := trajectory.ListSessions(root)
	if err != nil {
		return err
	}
	for _, s := range sessions {
		fmt.Println(s)
	}
	return nil
}

func runTrajectoryCleanup(args []string) error {
	fs := flag.NewFlagSet("trajectory cleanup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var root string
	fs.StringVar(&root, "dir", "", "trajectory root")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: metiq trajectory cleanup <session-id>")
	}
	return trajectory.Cleanup(root, fs.Arg(0))
}
