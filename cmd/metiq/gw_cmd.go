package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// ─── gw (gateway passthrough) ─────────────────────────────────────────────────

func runGW(args []string) error {
	fs := flag.NewFlagSet("gw", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var adminAddr, adminToken, bootstrapPath string
	var transport, controlTargetPubKey, controlSignerURL string
	var timeoutSec int
	var jsonOut bool
	fs.StringVar(&bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&adminAddr, "admin-addr", "", "admin API address (host:port)")
	fs.StringVar(&adminToken, "admin-token", "", "admin API bearer token")
	fs.StringVar(&transport, "transport", "auto", "gateway transport: auto, http, or nostr")
	fs.StringVar(&controlTargetPubKey, "control-target-pubkey", "", "target daemon pubkey for Nostr control RPC")
	fs.StringVar(&controlSignerURL, "control-signer-url", "", "caller signer override for Nostr control RPC (URL, env://, file://, bunker://, or direct key material)")
	fs.IntVar(&timeoutSec, "timeout", 30, "request timeout seconds")
	fs.BoolVar(&jsonOut, "json", true, "output raw JSON (default true)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	positional := fs.Args()
	if len(positional) == 0 {
		return fmt.Errorf("usage: metiq gw <method> [json-params]")
	}
	method := positional[0]

	// Collect JSON params: remaining positional args joined, or '{}' if none.
	var rawParams json.RawMessage
	if len(positional) > 1 {
		paramStr := strings.Join(positional[1:], " ")
		// Accept bare key=value pairs as a convenience shorthand.
		if !strings.HasPrefix(strings.TrimSpace(paramStr), "{") {
			// Try to build an object from key=value pairs.
			pairs := strings.Fields(paramStr)
			obj := map[string]string{}
			for _, p := range pairs {
				kv := strings.SplitN(p, "=", 2)
				if len(kv) == 2 {
					obj[kv[0]] = kv[1]
				}
			}
			b, _ := json.Marshal(obj)
			rawParams = b
		} else {
			rawParams = json.RawMessage(paramStr)
		}
	} else {
		rawParams = json.RawMessage("{}")
	}

	cl, err := resolveGWClientFn(transport, adminAddr, adminToken, bootstrapPath, controlTargetPubKey, controlSignerURL, time.Duration(timeoutSec)*time.Second)
	if err != nil {
		return err
	}
	if closer, ok := cl.(gatewayCloser); ok {
		defer closer.Close()
	}

	// Use cl.call; json.RawMessage marshals as-is so params stay intact.
	result, err := cl.call(method, rawParams)
	if err != nil {
		return fmt.Errorf("gw %s: %w", method, err)
	}

	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("%v\n", result)
	return nil
}
