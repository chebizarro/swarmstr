package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"metiq/internal/config"
	"os"
	"strings"
)

// ─── config subcommands ───────────────────────────────────────────────────────

func runConfigGet(args []string) error {
	fs := flag.NewFlagSet("config get", flag.ContinueOnError)
	var configPath string
	var jsonOut bool
	fs.StringVar(&configPath, "path", "", "config file path")
	fs.BoolVar(&jsonOut, "json", false, "output raw JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if configPath == "" {
		var err error
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve default config path: %w", err)
		}
	}

	doc, err := config.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// If a key was given, extract it; otherwise print the whole config.
	if key := fs.Arg(0); key != "" {
		raw, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		// Walk dot-separated keys.
		parts := strings.Split(key, ".")
		var cur any = m
		for _, p := range parts {
			mm, ok := cur.(map[string]any)
			if !ok {
				fmt.Fprintf(os.Stderr, "key %q not found\n", key)
				os.Exit(1)
			}
			cur, ok = mm[p]
			if !ok {
				fmt.Fprintf(os.Stderr, "key %q not found\n", key)
				os.Exit(1)
			}
		}
		if s, ok := cur.(string); ok && !jsonOut {
			fmt.Println(s)
			return nil
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cur)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(doc)
}

func runConfigValidate(args []string) error {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	var configPath string
	fs.StringVar(&configPath, "path", "", "config file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if configPath == "" {
		var err error
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return fmt.Errorf("resolve default config path: %w", err)
		}
	}

	doc, err := config.LoadConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("config invalid: %w", err)
	}

	if errs := config.ValidateConfigDoc(doc); len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  %v\n", e)
		}
		return fmt.Errorf("config has %d validation error(s)", len(errs))
	}

	fmt.Printf("config valid: %s\n", configPath)
	return nil
}

func runConfigPath(args []string) error {
	fs := flag.NewFlagSet("config path", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := config.DefaultConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(p)
	return nil
}
