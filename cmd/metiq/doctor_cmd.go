package main

import (
	"flag"
	"fmt"
	"os"
)

// ─── doctor ───────────────────────────────────────────────────────────────────

func runDoctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	var adminAddr, adminToken, bootstrapPath string
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	type check struct {
		name string
		ok   bool
		msg  string
	}
	var checks []check

	// Check: admin reachable.
	cl, err := resolveAdminClient(adminAddr, adminToken, bootstrapPath)
	if err != nil {
		checks = append(checks, check{"admin reachable", false, err.Error()})
	} else {
		result, err := cl.get("/health")
		if err != nil {
			checks = append(checks, check{"admin reachable", false, err.Error()})
		} else {
			checks = append(checks, check{"admin reachable", true, stringFieldAny(result, "status")})
		}
	}

	// Check: bootstrap file exists.
	if bootstrapPath == "" {
		if home, err2 := os.UserHomeDir(); err2 == nil {
			bootstrapPath = home + "/.config/metiq/bootstrap.json"
		}
	}
	if bootstrapPath != "" {
		if _, err2 := os.Stat(bootstrapPath); err2 == nil {
			checks = append(checks, check{"bootstrap file", true, bootstrapPath})
		} else {
			checks = append(checks, check{"bootstrap file", false, "not found at " + bootstrapPath})
		}
	}

	// Check: memory usage (from admin if reachable).
	if cl != nil {
		if result, err := cl.call("doctor.memory.status", map[string]any{}); err == nil {
			if index, ok := result["index"].(map[string]any); ok {
				docs := floatFieldAny(index, "entry_count")
				sessions := floatFieldAny(index, "session_count")
				checks = append(checks, check{"memory index", true, fmt.Sprintf("%.0f docs / %.0f sessions", docs, sessions)})
			} else {
				docs := floatFieldAny(result, "doc_count")
				checks = append(checks, check{"memory index", true, fmt.Sprintf("%.0f docs", docs)})
			}
		}
	}

	// Check: relay connectivity (from status).
	if cl != nil {
		if result, err := cl.get("/status"); err == nil {
			if relays, ok := result["relays"].([]any); ok {
				checks = append(checks, check{"relay config", len(relays) > 0,
					fmt.Sprintf("%d relay(s) configured", len(relays))})
			}
		}
	}

	if jsonOut {
		out := make([]map[string]any, len(checks))
		for i, c := range checks {
			out[i] = map[string]any{"name": c.name, "ok": c.ok, "message": c.msg}
		}
		return printJSON(map[string]any{"checks": out})
	}

	allOK := true
	for _, c := range checks {
		icon := "✓"
		if !c.ok {
			icon = "✗"
			allOK = false
		}
		fmt.Printf("  %s %s: %s\n", icon, c.name, c.msg)
	}
	fmt.Println()
	if allOK {
		fmt.Println("All checks passed.")
	} else {
		fmt.Println("Some checks failed.")
	}
	return nil
}
