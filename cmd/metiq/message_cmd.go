package main

import (
	"flag"
	"fmt"
	"strings"
)

func runMessage(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("message subcommands: send")
	}
	switch args[0] {
	case "send":
		return runMessageSend(args[1:])
	default:
		return fmt.Errorf("unknown message sub-command %q (send)", args[0])
	}
}

func runSend(args []string) error {
	return runMessageSend(args)
}

func runMessageSend(args []string) error {
	fs := flag.NewFlagSet("message send", flag.ContinueOnError)
	var flags metiqGatewayFlags
	var to, text string
	addMetiqGatewayFlags(fs, &flags)
	fs.StringVar(&to, "to", "", "target pubkey or channel/session identifier")
	fs.StringVar(&text, "text", "", "message text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if to == "" && fs.NArg() > 0 {
		to = fs.Arg(0)
	}
	if text == "" && fs.NArg() > 1 {
		text = strings.Join(fs.Args()[1:], " ")
	}
	if to == "" || text == "" {
		return fmt.Errorf("usage: metiq message send --to <target> --text <message>")
	}
	result, err := metiqAdminCall(flags, "send", map[string]any{"to": to, "message": text})
	if err != nil {
		return err
	}
	return printMetiqCallResult(result)
}
