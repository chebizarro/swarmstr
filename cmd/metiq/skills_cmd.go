package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// ─── skills ───────────────────────────────────────────────────────────────────

func runSkills(args []string) error {
	if len(args) == 0 {
		return runSkillsList(nil)
	}
	switch args[0] {
	case "list", "ls":
		return runSkillsList(args[1:])
	case "status":
		return runSkillsStatus(args[1:])
	case "check":
		return runSkillsCheck(args[1:])
	case "info":
		return runSkillsInfo(args[1:])
	case "install":
		return runSkillsInstall(args[1:])
	case "enable":
		return runSkillsEnable(args[1:])
	case "disable":
		return runSkillsDisable(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "skills subcommands: list, status, check, info, install, enable, disable\n")
		return fmt.Errorf("unknown subcommand: %s", args[0])
	}
}

func fetchSkillsStatus(adminAddr, adminToken, bootstrapPath, agentID string) (map[string]any, []map[string]any, error) {
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return nil, nil, err
	}
	params := map[string]any{}
	if agentID = strings.TrimSpace(agentID); agentID != "" {
		params["agent_id"] = agentID
	}
	result, err := cl.call("skills.status", params)
	if err != nil {
		return nil, nil, err
	}
	return result, normalizeSkillStatusEntries(result["skills"]), nil
}

func normalizeSkillStatusEntries(raw any) []map[string]any {
	switch v := raw.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, item := range v {
			entry, ok := item.(map[string]any)
			if ok {
				out = append(out, entry)
			}
		}
		return out
	default:
		return nil
	}
}

func findSkillStatusEntry(skills []map[string]any, needle string) (map[string]any, bool) {
	needle = strings.TrimSpace(needle)
	if needle == "" {
		return nil, false
	}
	for _, entry := range skills {
		if strings.EqualFold(stringField(entry, "id"), needle) || strings.EqualFold(stringField(entry, "skillKey"), needle) || strings.EqualFold(stringField(entry, "name"), needle) {
			return entry, true
		}
	}
	return nil, false
}

func missingSummary(entry map[string]any) string {
	if entry == nil {
		return ""
	}
	if stringField(entry, "status") == "disabled" {
		return "disabled"
	}
	if blocked, _ := entry["blockedByAllowlist"].(bool); blocked {
		return "blocked by allowlist"
	}
	missing, _ := entry["missing"].(map[string]any)
	parts := make([]string, 0)
	appendMissing := func(label string, raw any) {
		switch v := raw.(type) {
		case []string:
			if len(v) > 0 {
				parts = append(parts, label+": "+strings.Join(v, ","))
			}
		case []any:
			vals := make([]string, 0, len(v))
			for _, item := range v {
				if s := strings.TrimSpace(fmt.Sprintf("%v", item)); s != "" {
					vals = append(vals, s)
				}
			}
			if len(vals) > 0 {
				parts = append(parts, label+": "+strings.Join(vals, ","))
			}
		}
	}
	appendMissing("bins", missing["bins"])
	appendMissing("anyBins", missing["anyBins"])
	appendMissing("env", missing["env"])
	appendMissing("os", missing["os"])
	appendMissing("config", missing["config"])
	return strings.Join(parts, "; ")
}

func skillStatusHealthy(status string) bool {
	switch strings.TrimSpace(status) {
	case "ready", "always":
		return true
	default:
		return false
	}
}

func runSkillsList(args []string) error {
	fs := flag.NewFlagSet("skills list", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "", "agent id")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, all, err := fetchSkillsStatus(adminAddr, adminToken, bootstrapPath, agentID)
	if err != nil {
		return err
	}

	if jsonOut {
		return printJSON(result)
	}

	if len(all) == 0 {
		fmt.Println("no skills installed")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tDESCRIPTION")
	for _, sk := range all {
		id := stringField(sk, "id")
		status := stringField(sk, "status")
		desc := stringField(sk, "description")
		if len(desc) > 60 {
			desc = desc[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, status, desc)
	}
	return w.Flush()
}

func runSkillsStatus(args []string) error {
	fs := flag.NewFlagSet("skills status", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "", "agent id")
	if err := fs.Parse(args); err != nil {
		return err
	}

	result, _, err := fetchSkillsStatus(adminAddr, adminToken, bootstrapPath, agentID)
	if err != nil {
		return err
	}
	return printJSON(result)
}

func runSkillsCheck(args []string) error {
	fs := flag.NewFlagSet("skills check", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "", "agent id")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: metiq skills check [flags] [skill]")
	}
	_, skills, err := fetchSkillsStatus(adminAddr, adminToken, bootstrapPath, agentID)
	if err != nil {
		return err
	}
	selected := skills
	if fs.NArg() == 1 {
		entry, ok := findSkillStatusEntry(skills, fs.Arg(0))
		if !ok {
			return fmt.Errorf("skill not found: %s", fs.Arg(0))
		}
		selected = []map[string]any{entry}
	}
	issues := make([]map[string]any, 0)
	for _, entry := range selected {
		if !skillStatusHealthy(stringField(entry, "status")) {
			issues = append(issues, entry)
		}
	}
	if jsonOut {
		payload := map[string]any{"ok": len(issues) == 0, "skills": selected}
		if err := printJSON(payload); err != nil {
			return err
		}
		if len(issues) > 0 {
			return fmt.Errorf("%d skill checks failed", len(issues))
		}
		return nil
	}
	if len(selected) == 0 {
		fmt.Println("no skills installed")
		return nil
	}
	if len(issues) == 0 {
		if len(selected) == 1 {
			fmt.Printf("%s: %s\n", stringField(selected[0], "id"), stringField(selected[0], "status"))
		} else {
			fmt.Println("all checked skills ready")
		}
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tDETAILS")
	for _, entry := range issues {
		fmt.Fprintf(w, "%s\t%s\t%s\n", stringField(entry, "id"), stringField(entry, "status"), missingSummary(entry))
	}
	_ = w.Flush()
	return fmt.Errorf("%d skill checks failed", len(issues))
}

func runSkillsInfo(args []string) error {
	fs := flag.NewFlagSet("skills info", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "", "agent id")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq skills info [flags] <skill>")
	}
	_, skills, err := fetchSkillsStatus(adminAddr, adminToken, bootstrapPath, agentID)
	if err != nil {
		return err
	}
	entry, ok := findSkillStatusEntry(skills, fs.Arg(0))
	if !ok {
		return fmt.Errorf("skill not found: %s", fs.Arg(0))
	}
	if jsonOut {
		return printJSON(entry)
	}
	fmt.Printf("id: %s\n", stringField(entry, "id"))
	fmt.Printf("name: %s\n", stringField(entry, "name"))
	fmt.Printf("status: %s\n", stringField(entry, "status"))
	fmt.Printf("source: %s\n", stringField(entry, "source"))
	if desc := stringField(entry, "description"); desc != "" {
		fmt.Printf("description: %s\n", desc)
	}
	if when := stringField(entry, "whenToUse"); when != "" {
		fmt.Printf("whenToUse: %s\n", when)
	}
	if env := stringField(entry, "primaryEnv"); env != "" {
		fmt.Printf("primaryEnv: %s\n", env)
	}
	if installID := stringField(entry, "selectedInstallId"); installID != "" {
		fmt.Printf("selectedInstallId: %s\n", installID)
	}
	if path := stringField(entry, "filePath"); path != "" {
		fmt.Printf("filePath: %s\n", path)
	}
	if details := missingSummary(entry); details != "" {
		fmt.Printf("details: %s\n", details)
	}
	return nil
}

func runSkillsInstall(args []string) error {
	fs := flag.NewFlagSet("skills install", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var installID string
	var agentID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&installID, "install-id", "", "installer option id")
	fs.StringVar(&agentID, "agent", "main", "agent id")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq skills install [flags] <skill>")
	}
	if strings.TrimSpace(installID) == "" {
		return fmt.Errorf("--install-id is required")
	}

	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("skills.install", map[string]any{
		"agent_id":   agentID,
		"name":       fs.Arg(0),
		"install_id": installID,
	})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	if ok, _ := result["ok"].(bool); ok {
		fmt.Println(stringField(result, "message"))
		return nil
	}
	return fmt.Errorf("skills install failed: %s", stringField(result, "message"))
}

func runSkillsSetEnabled(args []string, enabled bool) error {
	label := "enable"
	verb := "Enabled"
	if !enabled {
		label = "disable"
		verb = "Disabled"
	}
	fs := flag.NewFlagSet("skills "+label, flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath, agentID string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&agentID, "agent", "main", "agent id")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: metiq skills %s [flags] <skill>", label)
	}
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		return err
	}
	result, err := cl.call("skills.update", map[string]any{
		"agent_id":  agentID,
		"skill_key": fs.Arg(0),
		"enabled":   enabled,
	})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("%s %s\n", verb, stringField(result, "skillKey"))
	return nil
}

func runSkillsEnable(args []string) error {
	return runSkillsSetEnabled(args, true)
}

func runSkillsDisable(args []string) error {
	return runSkillsSetEnabled(args, false)
}
