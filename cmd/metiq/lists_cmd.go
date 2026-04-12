package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func runLists(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("lists subcommands: get, put")
	}
	switch args[0] {
	case "get":
		return runListsGet(args[1:])
	case "put":
		return runListsPut(args[1:])
	default:
		return fmt.Errorf("unknown lists sub-command %q (get|put)", args[0])
	}
}

func runListsGet(args []string) error {
	fs := flag.NewFlagSet("lists get", flag.ContinueOnError)
	var name string
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&name, "name", "", "list name")
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("lists get requires --name")
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("list.get", map[string]any{"name": name})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	listDoc, _ := result["list"].(map[string]any)
	if listDoc == nil {
		return printJSON(result)
	}
	items, _ := listDoc["items"].([]any)
	fmt.Printf("list=%s items=%d\n", stringField(listDoc, "name"), len(items))
	for _, item := range items {
		if s, ok := item.(string); ok {
			fmt.Println(s)
		}
	}
	return nil
}

func runListsPut(args []string) error {
	fs := flag.NewFlagSet("lists put", flag.ContinueOnError)
	var name string
	var itemsCSV string
	var itemsFile string
	var expectedVersion int
	var expectedEvent string
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&name, "name", "", "list name")
	fs.StringVar(&itemsCSV, "item", "", "comma-separated list items")
	fs.StringVar(&itemsFile, "file", "", "newline-delimited list items file")
	fs.IntVar(&expectedVersion, "expected-version", -1, "optimistic version precondition")
	fs.StringVar(&expectedEvent, "expected-event", "", "optimistic expected event id")
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("lists put requires --name")
	}
	itemsSet := map[string]struct{}{}
	items := make([]string, 0)
	for _, part := range strings.Split(itemsCSV, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := itemsSet[part]; exists {
			continue
		}
		itemsSet[part] = struct{}{}
		items = append(items, part)
	}
	if strings.TrimSpace(itemsFile) != "" {
		raw, err := os.ReadFile(itemsFile)
		if err != nil {
			return fmt.Errorf("read items file: %w", err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, exists := itemsSet[line]; exists {
				continue
			}
			itemsSet[line] = struct{}{}
			items = append(items, line)
		}
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	params := map[string]any{"name": name, "items": items}
	if expectedVersion >= 0 {
		params["expected_version"] = expectedVersion
	}
	if strings.TrimSpace(expectedEvent) != "" {
		params["expected_event"] = strings.TrimSpace(expectedEvent)
	}
	result, err := cl.call("list.put", params)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	if eventID := stringField(result, "event_id"); eventID != "" {
		fmt.Printf("list=%s updated event_id=%s items=%d\n", name, eventID, len(items))
		return nil
	}
	return printJSON(result)
}
