package main

import "fmt"

func runTranscripts(args []string) error {
	if len(args) == 0 {
		return runSessionsList(args)
	}
	switch args[0] {
	case "list", "ls":
		return runSessionsList(args[1:])
	case "export":
		return runSessionsExport(args[1:])
	default:
		return fmt.Errorf("unknown transcripts sub-command %q (list|export)", args[0])
	}
}
