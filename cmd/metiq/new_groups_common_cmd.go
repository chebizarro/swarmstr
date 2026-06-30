package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

type metiqGatewayFlags struct {
	bootstrapPath string
	adminAddr     string
	adminToken    string
	jsonOut       bool
}

func addMetiqGatewayFlags(fs *flag.FlagSet, flags *metiqGatewayFlags) {
	fs.SetOutput(os.Stderr)
	fs.StringVar(&flags.bootstrapPath, "bootstrap", "", "bootstrap config path")
	fs.StringVar(&flags.adminAddr, "admin-addr", "", "admin API address")
	fs.StringVar(&flags.adminToken, "admin-token", "", "admin API token")
	fs.BoolVar(&flags.jsonOut, "json", jsonFlagDefault(), "output raw JSON")
}

func metiqAdminCall(flags metiqGatewayFlags, method string, params any) (map[string]any, error) {
	cl, err := resolveAdminClient(flags.adminAddr, flags.adminToken, flags.bootstrapPath)
	if err != nil {
		return nil, err
	}
	result, err := cl.call(method, params)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	return result, nil
}

func printMetiqCallResult(result map[string]any) error {
	return printJSON(result)
}

func metiqJSONOrKeyValueParams(args []string) (map[string]any, error) {
	params := map[string]any{}
	if len(args) == 0 {
		return params, nil
	}
	raw := strings.TrimSpace(strings.Join(args, " "))
	if strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			return nil, err
		}
		return params, nil
	}
	for _, arg := range args {
		kv := strings.SplitN(arg, "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			return nil, fmt.Errorf("expected JSON object or key=value params, got %q", arg)
		}
		params[kv[0]] = kv[1]
	}
	return params, nil
}

func requireMCPArg(fs *flag.FlagSet, usage string) error {
	if fs.NArg() == 0 {
		return fmt.Errorf("usage: %s", usage)
	}
	return nil
}
