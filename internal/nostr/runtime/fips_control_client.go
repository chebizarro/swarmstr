//go:build experimental_fips

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultFIPSDaemonControlTCP = "127.0.0.1:21210"

type fipsControlEndpoint struct {
	Network string
	Address string
}

type FIPSControlClientOptions struct {
	ControlSocket string
	DialTimeout   time.Duration
}

type FIPSControlClient struct {
	endpoint    fipsControlEndpoint
	dialTimeout time.Duration
}

type FIPSCacheSummary struct {
	Count        int
	MaxEntries   int
	FillRatio    float64
	DefaultTTLMS int64
	Expired      int
	AvgAgeMS     int64
	Entries      []FIPSCacheEntry
}

type FIPSCacheEntry struct {
	NodeAddr    string   `json:"node_addr"`
	DisplayName string   `json:"display_name"`
	IPv6Addr    string   `json:"ipv6_addr"`
	Depth       int      `json:"depth"`
	Coords      []string `json:"coords"`
	AgeMS       int64    `json:"age_ms"`
	LastUsedMS  int64    `json:"last_used_ms"`
	PathMTU     int      `json:"path_mtu,omitempty"`
}

type FIPSRoutingSummary struct {
	CoordCacheEntries      int
	IdentityCacheEntries   int
	PendingLookupsCount    int
	PendingLookups         []FIPSPendingLookup
	PendingTUNDestinations int
	PendingTUNPackets      int
	RecentRequests         int
	RetriesCount           int
	Retries                []FIPSRoutingRetry
}

type FIPSPendingLookup struct {
	Target      string `json:"target"`
	DisplayName string `json:"display_name"`
	InitiatedMS int64  `json:"initiated_ms"`
	LastSentMS  int64  `json:"last_sent_ms"`
	Attempt     int    `json:"attempt"`
	AgeMS       int64  `json:"age_ms"`
}

type FIPSRoutingRetry struct {
	NodeAddr      string `json:"node_addr"`
	DisplayName   string `json:"display_name"`
	RetryCount    int    `json:"retry_count"`
	RetryAfterMS  int64  `json:"retry_after_ms"`
	AutoReconnect bool   `json:"auto_reconnect"`
}

func NewFIPSControlClient(opts FIPSControlClientOptions) (*FIPSControlClient, error) {
	endpoint, err := parseFIPSControlEndpoint(opts.ControlSocket)
	if err != nil {
		return nil, err
	}
	dialTimeout := opts.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 2 * time.Second
	}
	return &FIPSControlClient{endpoint: endpoint, dialTimeout: dialTimeout}, nil
}

func (c *FIPSControlClient) Endpoint() (network, address string) {
	if c == nil {
		return "", ""
	}
	return c.endpoint.Network, c.endpoint.Address
}

func (c *FIPSControlClient) ShowCache(ctx context.Context) (FIPSCacheSummary, error) {
	var out FIPSCacheSummary
	data, err := c.query(ctx, "show_cache", nil)
	if err != nil {
		return out, err
	}
	return parseFIPSCacheSummary(data)
}

func (c *FIPSControlClient) ShowRouting(ctx context.Context) (FIPSRoutingSummary, error) {
	var out FIPSRoutingSummary
	data, err := c.query(ctx, "show_routing", nil)
	if err != nil {
		return out, err
	}
	return parseFIPSRoutingSummary(data)
}

func (c *FIPSControlClient) query(ctx context.Context, command string, params any) (json.RawMessage, error) {
	if c == nil {
		return nil, fmt.Errorf("fips control client: nil client")
	}
	if command == "" {
		return nil, fmt.Errorf("fips control client: command is required")
	}
	dialer := net.Dialer{Timeout: c.dialTimeout}
	conn, err := dialer.DialContext(ctx, c.endpoint.Network, c.endpoint.Address)
	if err != nil {
		return nil, fmt.Errorf("fips control client: dial %s %s: %w", c.endpoint.Network, c.endpoint.Address, err)
	}
	defer conn.Close()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	req := struct {
		Command string `json:"command"`
		Params  any    `json:"params,omitempty"`
	}{Command: command, Params: params}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("fips control client: write request: %w", err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("fips control client: read response: %w", err)
	}
	return parseFIPSControlData(line)
}

func parseFIPSControlEndpoint(value string) (fipsControlEndpoint, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultFIPSControlEndpoint(), nil
	}
	if rest, ok := strings.CutPrefix(value, "unix://"); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return fipsControlEndpoint{}, fmt.Errorf("fips control endpoint: empty unix address")
		}
		return fipsControlEndpoint{Network: "unix", Address: rest}, nil
	}
	if rest, ok := strings.CutPrefix(value, "tcp://"); ok {
		rest = strings.TrimSpace(rest)
		if rest == "" {
			return fipsControlEndpoint{}, fmt.Errorf("fips control endpoint: empty tcp address")
		}
		return fipsControlEndpoint{Network: "tcp", Address: rest}, nil
	}
	if runtime.GOOS == "windows" {
		if _, err := strconv.Atoi(value); err == nil {
			return fipsControlEndpoint{Network: "tcp", Address: net.JoinHostPort("127.0.0.1", value)}, nil
		}
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.ContainsAny(value, `/\\`) {
		return fipsControlEndpoint{Network: "unix", Address: value}, nil
	}
	if host, port, err := net.SplitHostPort(value); err == nil && host != "" && port != "" {
		return fipsControlEndpoint{Network: "tcp", Address: net.JoinHostPort(host, port)}, nil
	}
	if strings.Count(value, ":") == 1 {
		host, port, ok := strings.Cut(value, ":")
		if ok && strings.TrimSpace(host) != "" && strings.TrimSpace(port) != "" {
			return fipsControlEndpoint{Network: "tcp", Address: value}, nil
		}
	}
	return fipsControlEndpoint{}, fmt.Errorf("fips control endpoint: cannot parse %q", value)
}

func defaultFIPSControlEndpoint() fipsControlEndpoint {
	if runtime.GOOS == "windows" {
		return fipsControlEndpoint{Network: "tcp", Address: defaultFIPSDaemonControlTCP}
	}
	if dirExists("/run/fips") {
		return fipsControlEndpoint{Network: "unix", Address: "/run/fips/control.sock"}
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); xdg != "" && dirExists(xdg) {
		return fipsControlEndpoint{Network: "unix", Address: filepath.Join(xdg, "fips", "control.sock")}
	}
	return fipsControlEndpoint{Network: "unix", Address: "/tmp/fips-control.sock"}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func parseFIPSControlData(payload []byte) (json.RawMessage, error) {
	var env struct {
		Status  string          `json:"status"`
		Data    json.RawMessage `json:"data"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, err
	}
	if env.Status != "ok" {
		if env.Message == "" {
			env.Message = "control command failed"
		}
		return nil, fmt.Errorf("fips control: %s", env.Message)
	}
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return json.RawMessage(`{}`), nil
	}
	return env.Data, nil
}

func parseFIPSCacheSummary(data json.RawMessage) (FIPSCacheSummary, error) {
	var raw struct {
		Count        *int            `json:"count"`
		MaxEntries   int             `json:"max_entries"`
		FillRatio    float64         `json:"fill_ratio"`
		DefaultTTLMS int64           `json:"default_ttl_ms"`
		Expired      int             `json:"expired"`
		AvgAgeMS     int64           `json:"avg_age_ms"`
		Entries      json.RawMessage `json:"entries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return FIPSCacheSummary{}, err
	}
	entries, scalarCount, err := decodeCompatList[FIPSCacheEntry](raw.Entries)
	if err != nil {
		return FIPSCacheSummary{}, fmt.Errorf("parse show_cache entries: %w", err)
	}
	count := len(entries)
	if raw.Count != nil {
		count = *raw.Count
	} else if scalarCount != nil {
		count = *scalarCount
	}
	return FIPSCacheSummary{
		Count:        count,
		MaxEntries:   raw.MaxEntries,
		FillRatio:    raw.FillRatio,
		DefaultTTLMS: raw.DefaultTTLMS,
		Expired:      raw.Expired,
		AvgAgeMS:     raw.AvgAgeMS,
		Entries:      entries,
	}, nil
}

func parseFIPSRoutingSummary(data json.RawMessage) (FIPSRoutingSummary, error) {
	var raw struct {
		CoordCacheEntries      int             `json:"coord_cache_entries"`
		IdentityCacheEntries   int             `json:"identity_cache_entries"`
		PendingLookupsCount    *int            `json:"pending_lookups_count"`
		PendingLookups         json.RawMessage `json:"pending_lookups"`
		PendingTUNDestinations int             `json:"pending_tun_destinations"`
		PendingTUNPackets      int             `json:"pending_tun_packets"`
		RecentRequests         int             `json:"recent_requests"`
		RetriesCount           *int            `json:"retries_count"`
		Retries                json.RawMessage `json:"retries"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return FIPSRoutingSummary{}, err
	}
	lookups, lookupScalarCount, err := decodeCompatList[FIPSPendingLookup](raw.PendingLookups)
	if err != nil {
		return FIPSRoutingSummary{}, fmt.Errorf("parse show_routing pending_lookups: %w", err)
	}
	retries, retryScalarCount, err := decodeCompatList[FIPSRoutingRetry](raw.Retries)
	if err != nil {
		return FIPSRoutingSummary{}, fmt.Errorf("parse show_routing retries: %w", err)
	}
	lookupCount := len(lookups)
	if raw.PendingLookupsCount != nil {
		lookupCount = *raw.PendingLookupsCount
	} else if lookupScalarCount != nil {
		lookupCount = *lookupScalarCount
	}
	retryCount := len(retries)
	if raw.RetriesCount != nil {
		retryCount = *raw.RetriesCount
	} else if retryScalarCount != nil {
		retryCount = *retryScalarCount
	}
	return FIPSRoutingSummary{
		CoordCacheEntries:      raw.CoordCacheEntries,
		IdentityCacheEntries:   raw.IdentityCacheEntries,
		PendingLookupsCount:    lookupCount,
		PendingLookups:         lookups,
		PendingTUNDestinations: raw.PendingTUNDestinations,
		PendingTUNPackets:      raw.PendingTUNPackets,
		RecentRequests:         raw.RecentRequests,
		RetriesCount:           retryCount,
		Retries:                retries,
	}, nil
}

func decodeCompatList[T any](raw json.RawMessage) ([]T, *int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil
	}
	var entries []T
	if err := json.Unmarshal(raw, &entries); err == nil {
		return entries, nil, nil
	}
	var count int
	if err := json.Unmarshal(raw, &count); err == nil {
		return nil, &count, nil
	}
	var keyed map[string]T
	if err := json.Unmarshal(raw, &keyed); err == nil {
		entries = make([]T, 0, len(keyed))
		for _, entry := range keyed {
			entries = append(entries, entry)
		}
		return entries, nil, nil
	}
	var single T
	if err := json.Unmarshal(raw, &single); err == nil {
		return []T{single}, nil, nil
	}
	return nil, nil, fmt.Errorf("unsupported JSON shape")
}
