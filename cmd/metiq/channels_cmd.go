package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
)

// ─── models ──────────────────────────────────────────────────────────────────

func runModels(args []string) error {
	if len(args) == 0 {
		return runModelsList(nil)
	}
	switch args[0] {
	case "list":
		return runModelsList(args[1:])
	case "set":
		return runModelsSet(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "models subcommands: list, set\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runModelsList(args []string) error {
	fs := flag.NewFlagSet("models list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "", "agent ID (default: default agent)")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("models.list", map[string]any{"agent_id": agentID})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	models, _ := result["models"].([]any)
	if len(models) == 0 {
		fmt.Println("no models available")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tPROVIDER\tCONTEXT")
	for _, m := range models {
		mod, ok := m.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(mod, "id")
		prov := stringField(mod, "provider")
		ctx := ""
		if v, ok := mod["context_window"].(float64); ok {
			ctx = fmt.Sprintf("%dk", int(v/1000))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, prov, ctx)
	}
	return w.Flush()
}

func runModelsSet(args []string) error {
	fs := flag.NewFlagSet("models set", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "", "agent ID (default: default agent)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: metiq models set <model-id> [--agent <id>]")
	}
	modelID := fs.Arg(0)

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	_, err = cl.call("agents.update", map[string]any{
		"agent_id": agentID,
		"model":    modelID,
	})
	if err != nil {
		return err
	}
	fmt.Printf("default model set to: %s\n", modelID)
	return nil
}

// ─── channels ────────────────────────────────────────────────────────────────

func runChannels(args []string) error {
	if len(args) == 0 {
		return runChannelsList(nil)
	}
	switch args[0] {
	case "list", "ls":
		return runChannelsList(args[1:])
	case "status":
		return runChannelsStatus(args[1:])
	case "join", "add":
		return runChannelsJoin(args[1:])
	case "leave", "remove", "rm":
		return runChannelsLeave(args[1:])
	case "send":
		return runChannelsSend(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "channels subcommands: list, status, join, add, leave, send\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func runChannelsList(args []string) error {
	fs := flag.NewFlagSet("channels list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("channels.status", map[string]any{})
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	chans, _ := result["channels"].([]any)
	if len(chans) == 0 {
		fmt.Println("no channels configured")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tKIND\tSTATUS")
	for _, c := range chans {
		ch, ok := c.(map[string]any)
		if !ok {
			continue
		}
		id := stringField(ch, "id")
		kind := stringField(ch, "kind")
		if kind == "" {
			kind = stringFieldAny(ch, "channel")
		}
		if kind == "" {
			kind = id
		}
		status := stringField(ch, "status")
		if status == "" {
			status = channelStatusLabel(ch)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, kind, status)
	}
	return w.Flush()
}

func runChannelsJoin(args []string) error {
	fs := flag.NewFlagSet("channels join", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, channelType, groupAddress string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&channelType, "type", "nip29-group", "channel type")
	fs.StringVar(&groupAddress, "group-address", "", "NIP-29 group address: relayHost'groupID")
	fs.StringVar(&groupAddress, "group", "", "alias for --group-address")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if groupAddress == "" && fs.NArg() > 0 {
		groupAddress = fs.Arg(0)
	}
	if groupAddress == "" {
		var err error
		groupAddress, err = promptText(os.Stdin, os.Stdout, "NIP-29 group address (relayHost'groupID)", "", false)
		if err != nil {
			return err
		}
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("channels.join", map[string]any{"type": channelType, "group_address": groupAddress})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	printSuccess("channel joined: %s", stringFieldAny(result, "channel_id"))
	return nil
}

func runChannelsLeave(args []string) error {
	fs := flag.NewFlagSet("channels leave", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, channelID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&channelID, "channel", "", "channel ID")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if channelID == "" && fs.NArg() > 0 {
		channelID = fs.Arg(0)
	}
	if channelID == "" {
		return fmt.Errorf("usage: metiq channels leave <channel-id>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("channels.leave", map[string]any{"channel_id": channelID})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	printSuccess("channel left: %s", channelID)
	return nil
}

func runChannelsSend(args []string) error {
	fs := flag.NewFlagSet("channels send", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, channelID, text string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&channelID, "channel", "", "channel ID")
	fs.StringVar(&text, "text", "", "message text")
	fs.BoolVar(&jsonOut, "json", jsonFlagDefault(), "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if channelID == "" && fs.NArg() > 0 {
		channelID = fs.Arg(0)
	}
	if text == "" && fs.NArg() > 1 {
		text = fs.Arg(1)
	}
	if channelID == "" || text == "" {
		return fmt.Errorf("usage: metiq channels send <channel-id> <text>")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("channels.send", map[string]any{"channel_id": channelID, "text": text})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	printSuccess("message sent to %s", channelID)
	return nil
}

func runChannelsStatus(args []string) error {
	fs := flag.NewFlagSet("channels status", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}

	result, err := cl.call("channels.status", map[string]any{})
	if err != nil {
		return err
	}
	return printJSON(result)
}
