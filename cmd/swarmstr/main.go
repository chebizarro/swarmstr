package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"swarmstr/internal/config"
	"swarmstr/internal/memory"
	nostruntime "swarmstr/internal/nostr/runtime"
)

func main() {
	var bootstrapPath string
	flag.StringVar(&bootstrapPath, "bootstrap", "", "path to bootstrap config JSON")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		return
	}

	switch args[0] {
	case "plan":
		fmt.Println("docs/PORT_PLAN.md")
	case "bootstrap-check":
		cfg, err := config.LoadBootstrap(bootstrapPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bootstrap invalid: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("bootstrap ok: relays=%d state_kind=%d transcript_kind=%d\n",
			len(cfg.Relays), cfg.EffectiveStateKind(), cfg.EffectiveTranscriptKind())
	case "dm-send":
		if err := runDMSend(bootstrapPath, args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "dm-send failed: %v\n", err)
			os.Exit(1)
		}
	case "memory-search":
		if err := runMemorySearch(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "memory-search failed: %v\n", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func runDMSend(bootstrapPath string, args []string) error {
	fs := flag.NewFlagSet("dm-send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var to string
	var text string
	var timeoutSec int
	fs.StringVar(&to, "to", "", "recipient npub/hex pubkey")
	fs.StringVar(&text, "text", "", "plaintext message")
	fs.IntVar(&timeoutSec, "timeout", 15, "publish timeout seconds")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if to == "" || text == "" {
		return fmt.Errorf("dm-send requires --to and --text")
	}

	cfg, err := config.LoadBootstrap(bootstrapPath)
	if err != nil {
		return err
	}
	privateKey, err := config.ResolvePrivateKey(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	eventID, err := nostruntime.SendDMOnce(ctx, privateKey, cfg.Relays, to, text)
	if err != nil {
		return err
	}
	fmt.Printf("dm published event_id=%s\n", eventID)
	return nil
}

func runMemorySearch(args []string) error {
	fs := flag.NewFlagSet("memory-search", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var query string
	var limit int
	fs.StringVar(&query, "q", "", "search query")
	fs.IntVar(&limit, "limit", 10, "max results")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("memory-search requires --q")
	}

	index, err := memory.OpenIndex("")
	if err != nil {
		return err
	}
	results := index.Search(query, limit)
	for _, r := range results {
		fmt.Printf("[%s] session=%s topic=%s text=%q\n", r.MemoryID, r.SessionID, r.Topic, r.Text)
	}
	if len(results) == 0 {
		fmt.Println("no results")
	}
	return nil
}

func usage() {
	fmt.Println("swarmstr <command>")
	fmt.Println("commands:")
	fmt.Println("  plan               print implementation plan path")
	fmt.Println("  bootstrap-check    validate local bootstrap config")
	fmt.Println("  dm-send            send one NIP-04 DM (--to --text)")
	fmt.Println("  memory-search      search local memory index (--q [--limit])")
}
