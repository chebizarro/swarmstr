package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"swarmstr/internal/config"
)

// adminClient is a minimal HTTP client for the swarmstrd admin API.
type adminClient struct {
	addr  string
	token string
}

// resolveAdminClient builds an adminClient from the --admin-addr flag (or env
// SWARMSTR_ADMIN_ADDR) and the --admin-token flag (or env SWARMSTR_ADMIN_TOKEN).
// If neither is set, it falls back to admin_listen_addr in the bootstrap config.
func resolveAdminClient(addrFlag, tokenFlag, bootstrapPath string) (*adminClient, error) {
	addr := addrFlag
	token := tokenFlag

	if addr == "" {
		addr = os.Getenv("SWARMSTR_ADMIN_ADDR")
	}
	if token == "" {
		token = os.Getenv("SWARMSTR_ADMIN_TOKEN")
	}

	// Fall back to bootstrap config (lenient read — no validation).
	if addr == "" {
		bsPath := bootstrapPath
		if bsPath == "" {
			if p, err := config.DefaultBootstrapPath(); err == nil {
				bsPath = p
			}
		}
		if bsPath != "" {
			if raw, err := os.ReadFile(bsPath); err == nil {
				var m map[string]any
				if json.Unmarshal(raw, &m) == nil {
					if v, ok := m["admin_listen_addr"].(string); ok {
						addr = v
					}
					if token == "" {
						if v, ok := m["admin_token"].(string); ok {
							token = v
						}
					}
				}
			}
		}
	}

	if addr == "" {
		return nil, fmt.Errorf(
			"admin API address not configured.\n" +
				"Set admin_listen_addr in ~/.swarmstr/bootstrap.json,\n" +
				"or pass --admin-addr, or set SWARMSTR_ADMIN_ADDR.",
		)
	}

	return &adminClient{addr: addr, token: token}, nil
}

func (c *adminClient) baseURL() string {
	return "http://" + c.addr
}

// call invokes a gateway method via POST /call.
func (c *adminClient) call(method string, params any) (map[string]any, error) {
	body, err := json.Marshal(map[string]any{
		"method": method,
		"params": params,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL()+"/call", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach daemon at %s: %w", c.addr, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var envelope struct {
		OK     bool           `json:"ok"`
		Error  string         `json:"error,omitempty"`
		Result map[string]any `json:"result,omitempty"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("invalid response from daemon: %w", err)
	}
	if !envelope.OK {
		msg := envelope.Error
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("daemon error: %s", msg)
	}
	return envelope.Result, nil
}

// get performs an HTTP GET and decodes the JSON response.
func (c *adminClient) get(path string) (map[string]any, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL()+path, nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	cl := &http.Client{Timeout: 10 * time.Second}
	resp, err := cl.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach daemon at %s: %w", c.addr, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("invalid response: %w", err)
	}
	return result, nil
}

// printJSON pretty-prints v as indented JSON to stdout.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// stringField safely extracts a string from a map.
func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// floatField safely extracts a float64 from a map.
func floatField(m map[string]any, key string) float64 {
	if m == nil {
		return 0
	}
	if v, ok := m[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}
