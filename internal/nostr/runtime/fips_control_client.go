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
	Forwarding             FIPSForwardingCounters
	Discovery              FIPSDiscoveryCounters
	ErrorSignals           FIPSErrorSignalCounters
	Congestion             FIPSCongestionCounters
}

// FIPSStatusSummary mirrors the FIPS v0.4.0 show_status data payload.
type FIPSStatusSummary struct {
	Version             string                 `json:"version"`
	Npub                string                 `json:"npub"`
	NodeAddr            string                 `json:"node_addr"`
	IPv6Addr            string                 `json:"ipv6_addr"`
	State               string                 `json:"state"`
	IsLeafOnly          bool                   `json:"is_leaf_only"`
	IsRoot              bool                   `json:"is_root"`
	Root                string                 `json:"root"`
	Persistent          bool                   `json:"persistent"`
	PeerCount           int                    `json:"peer_count"`
	SessionCount        int                    `json:"session_count"`
	LinkCount           int                    `json:"link_count"`
	TransportCount      int                    `json:"transport_count"`
	ConnectionCount     int                    `json:"connection_count"`
	TransportPeerCounts map[string]int         `json:"transport_peer_counts"`
	TUNState            string                 `json:"tun_state"`
	TUNName             string                 `json:"tun_name"`
	EffectiveIPv6MTU    int                    `json:"effective_ipv6_mtu"`
	ControlSocket       string                 `json:"control_socket"`
	PID                 json.RawMessage        `json:"pid"`
	ExePath             string                 `json:"exe_path"`
	UptimeSecs          json.RawMessage        `json:"uptime_secs"`
	EstimatedMeshSize   *int                   `json:"estimated_mesh_size"`
	Forwarding          FIPSForwardingCounters `json:"forwarding"`
	Sparklines          map[string][]*float64  `json:"sparklines"`
}

type FIPSForwardingCounters struct {
	DecodeErrorBytes       int64 `json:"decode_error_bytes"`
	DecodeErrorPackets     int64 `json:"decode_error_packets"`
	DeliveredBytes         int64 `json:"delivered_bytes"`
	DeliveredPackets       int64 `json:"delivered_packets"`
	DropMTUExceededBytes   int64 `json:"drop_mtu_exceeded_bytes"`
	DropMTUExceededPackets int64 `json:"drop_mtu_exceeded_packets"`
	DropNoRouteBytes       int64 `json:"drop_no_route_bytes"`
	DropNoRoutePackets     int64 `json:"drop_no_route_packets"`
	DropSendErrorBytes     int64 `json:"drop_send_error_bytes"`
	DropSendErrorPackets   int64 `json:"drop_send_error_packets"`
	ForwardedBytes         int64 `json:"forwarded_bytes"`
	ForwardedPackets       int64 `json:"forwarded_packets"`
	OriginatedBytes        int64 `json:"originated_bytes"`
	OriginatedPackets      int64 `json:"originated_packets"`
	ReceivedBytes          int64 `json:"received_bytes"`
	ReceivedPackets        int64 `json:"received_packets"`
	RouteCrosslinkAscend   int64 `json:"route_crosslink_ascend"`
	RouteCrosslinkDescend  int64 `json:"route_crosslink_descend"`
	RouteDirectPeer        int64 `json:"route_direct_peer"`
	RouteTreeDown          int64 `json:"route_tree_down"`
	RouteTreeDownCross     int64 `json:"route_tree_down_cross"`
	RouteTreeUp            int64 `json:"route_tree_up"`
	TTLExhaustedBytes      int64 `json:"ttl_exhausted_bytes"`
	TTLExhaustedPackets    int64 `json:"ttl_exhausted_packets"`
}

type FIPSDiscoveryCounters struct {
	ReqBackoffSuppressed  int64 `json:"req_backoff_suppressed"`
	ReqBloomMiss          int64 `json:"req_bloom_miss"`
	ReqDecodeError        int64 `json:"req_decode_error"`
	ReqDedupCacheFull     int64 `json:"req_dedup_cache_full"`
	ReqDeduplicated       int64 `json:"req_deduplicated"`
	ReqDuplicate          int64 `json:"req_duplicate"`
	ReqFallbackForwarded  int64 `json:"req_fallback_forwarded"`
	ReqForwardRateLimited int64 `json:"req_forward_rate_limited"`
	ReqForwarded          int64 `json:"req_forwarded"`
	ReqInitiated          int64 `json:"req_initiated"`
	ReqNoTreePeer         int64 `json:"req_no_tree_peer"`
	ReqReceived           int64 `json:"req_received"`
	ReqTargetIsUs         int64 `json:"req_target_is_us"`
	ReqTTLExhausted       int64 `json:"req_ttl_exhausted"`
	RespAccepted          int64 `json:"resp_accepted"`
	RespDecodeError       int64 `json:"resp_decode_error"`
	RespForwarded         int64 `json:"resp_forwarded"`
	RespIdentityMiss      int64 `json:"resp_identity_miss"`
	RespNoRoute           int64 `json:"resp_no_route"`
	RespProofFailed       int64 `json:"resp_proof_failed"`
	RespReceived          int64 `json:"resp_received"`
	RespTimedOut          int64 `json:"resp_timed_out"`
}

type FIPSErrorSignalCounters struct {
	CoordsRequired int64 `json:"coords_required"`
	MTUExceeded    int64 `json:"mtu_exceeded"`
	PathBroken     int64 `json:"path_broken"`
}

type FIPSCongestionCounters struct {
	CEForwarded        int64 `json:"ce_forwarded"`
	CEReceived         int64 `json:"ce_received"`
	CongestionDetected int64 `json:"congestion_detected"`
	KernelDropEvents   int64 `json:"kernel_drop_events"`
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

type FIPSMetricsSummary struct {
	Forwarding map[string]int64 `json:"forwarding"`
	Discovery  map[string]int64 `json:"discovery"`
	Tree       map[string]int64 `json:"tree"`
	Bloom      map[string]int64 `json:"bloom"`
	Congestion map[string]int64 `json:"congestion"`
	Errors     map[string]int64 `json:"errors"`
}

type FIPSTreeSummary struct {
	MyNodeAddr          string         `json:"my_node_addr"`
	Root                string         `json:"root"`
	RootNpub            string         `json:"root_npub"`
	IsRoot              bool           `json:"is_root"`
	Depth               int            `json:"depth"`
	MyCoords            []string       `json:"my_coords"`
	Parent              string         `json:"parent"`
	ParentDisplayName   string         `json:"parent_display_name"`
	DeclarationSequence int64          `json:"declaration_sequence"`
	DeclarationSigned   bool           `json:"declaration_signed"`
	PeerTreeCount       int            `json:"peer_tree_count"`
	Peers               []FIPSTreePeer `json:"peers"`
	Stats               FIPSTreeStats  `json:"stats"`
}

type FIPSTreePeer struct {
	NodeAddr       string   `json:"node_addr"`
	DisplayName    string   `json:"display_name"`
	Parent         string   `json:"parent"`
	Root           string   `json:"root"`
	Depth          int      `json:"depth"`
	Coords         []string `json:"coords"`
	Sequence       int64    `json:"sequence"`
	AgeMS          int64    `json:"age_ms"`
	EffectiveDepth *float64 `json:"effective_depth"`
}

type FIPSTreeStats struct {
	Accepted           int64 `json:"accepted"`
	AddrMismatch       int64 `json:"addr_mismatch"`
	AncestryChanged    int64 `json:"ancestry_changed"`
	AncestryInvalid    int64 `json:"ancestry_invalid"`
	DecodeError        int64 `json:"decode_error"`
	FlapDampened       int64 `json:"flap_dampened"`
	LoopDetected       int64 `json:"loop_detected"`
	OutboundSignFailed int64 `json:"outbound_sign_failed"`
	ParentLosses       int64 `json:"parent_losses"`
	ParentSwitched     int64 `json:"parent_switched"`
	ParentSwitches     int64 `json:"parent_switches"`
	RateLimited        int64 `json:"rate_limited"`
	Received           int64 `json:"received"`
	SendFailed         int64 `json:"send_failed"`
	Sent               int64 `json:"sent"`
	SigFailed          int64 `json:"sig_failed"`
	Stale              int64 `json:"stale"`
	UnknownPeer        int64 `json:"unknown_peer"`
}

type FIPSBloomSummary struct {
	OwnNodeAddr          string                `json:"own_node_addr"`
	IsLeafOnly           bool                  `json:"is_leaf_only"`
	Sequence             int64                 `json:"sequence"`
	LeafDependentCount   int                   `json:"leaf_dependent_count"`
	LeafDependents       []string              `json:"leaf_dependents"`
	PeerFilters          []FIPSBloomPeerFilter `json:"peer_filters"`
	UptreeFillRatio      *float64              `json:"uptree_fill_ratio"`
	UptreeEstimatedCount *int64                `json:"uptree_estimated_count"`
	Stats                FIPSBloomStats        `json:"stats"`
}

type FIPSBloomPeerFilter struct {
	NodeAddr       string  `json:"node_addr"`
	DisplayName    string  `json:"display_name"`
	FillRatio      float64 `json:"fill_ratio"`
	EstimatedCount int64   `json:"estimated_count"`
	Sequence       int64   `json:"sequence"`
	AgeMS          int64   `json:"age_ms"`
}

type FIPSBloomStats struct {
	Accepted           int64 `json:"accepted"`
	DebounceSuppressed int64 `json:"debounce_suppressed"`
	DecodeError        int64 `json:"decode_error"`
	FillExceeded       int64 `json:"fill_exceeded"`
	Invalid            int64 `json:"invalid"`
	NonV1              int64 `json:"non_v1"`
	Received           int64 `json:"received"`
	SendFailed         int64 `json:"send_failed"`
	Sent               int64 `json:"sent"`
	Stale              int64 `json:"stale"`
	UnknownPeer        int64 `json:"unknown_peer"`
}

type FIPSPeersSummary struct {
	Peers []FIPSPeerSummary `json:"peers"`
}

type FIPSPeerSummary struct {
	NodeAddr       string          `json:"node_addr"`
	Npub           string          `json:"npub"`
	DisplayName    string          `json:"display_name"`
	IPv6Addr       string          `json:"ipv6_addr"`
	Connectivity   string          `json:"connectivity"`
	LinkID         string          `json:"link_id"`
	Direction      string          `json:"direction"`
	TransportAddr  string          `json:"transport_addr"`
	TransportType  string          `json:"transport_type"`
	IsParent       bool            `json:"is_parent"`
	IsChild        bool            `json:"is_child"`
	TreeDepth      *int            `json:"tree_depth"`
	EffectiveDepth *float64        `json:"effective_depth"`
	Stats          json.RawMessage `json:"stats"`
	Noise          json.RawMessage `json:"noise"`
	CurrentKBit    *int            `json:"current_k_bit"`
	MMP            json.RawMessage `json:"mmp"`
}

type FIPSTransportsSummary struct {
	Transports []FIPSTransportSummary `json:"transports"`
}

type FIPSTransportSummary struct {
	TransportID   string          `json:"transport_id"`
	Type          string          `json:"type"`
	State         string          `json:"state"`
	MTU           int             `json:"mtu"`
	Name          string          `json:"name"`
	LocalAddr     string          `json:"local_addr"`
	TorMode       string          `json:"tor_mode,omitempty"`
	OnionAddress  string          `json:"onion_address,omitempty"`
	TorMonitoring *bool           `json:"tor_monitoring,omitempty"`
	Stats         json.RawMessage `json:"stats"`
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

func (c *FIPSControlClient) ShowStatus(ctx context.Context) (FIPSStatusSummary, error) {
	var out FIPSStatusSummary
	data, err := c.query(ctx, "show_status", nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *FIPSControlClient) ShowMetrics(ctx context.Context) (FIPSMetricsSummary, error) {
	var out FIPSMetricsSummary
	data, err := c.query(ctx, "show_metrics", nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *FIPSControlClient) ShowRouting(ctx context.Context) (FIPSRoutingSummary, error) {
	var out FIPSRoutingSummary
	data, err := c.query(ctx, "show_routing", nil)
	if err != nil {
		return out, err
	}
	return parseFIPSRoutingSummary(data)
}

func (c *FIPSControlClient) ShowTree(ctx context.Context) (FIPSTreeSummary, error) {
	var out FIPSTreeSummary
	data, err := c.query(ctx, "show_tree", nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *FIPSControlClient) ShowBloom(ctx context.Context) (FIPSBloomSummary, error) {
	var out FIPSBloomSummary
	data, err := c.query(ctx, "show_bloom", nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *FIPSControlClient) ShowPeers(ctx context.Context) (FIPSPeersSummary, error) {
	var out FIPSPeersSummary
	data, err := c.query(ctx, "show_peers", nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
}

func (c *FIPSControlClient) ShowTransports(ctx context.Context) (FIPSTransportsSummary, error) {
	var out FIPSTransportsSummary
	data, err := c.query(ctx, "show_transports", nil)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	return out, nil
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
		CoordCacheEntries      int                     `json:"coord_cache_entries"`
		IdentityCacheEntries   int                     `json:"identity_cache_entries"`
		PendingLookupsCount    *int                    `json:"pending_lookups_count"`
		PendingLookups         json.RawMessage         `json:"pending_lookups"`
		PendingTUNDestinations int                     `json:"pending_tun_destinations"`
		PendingTUNPackets      int                     `json:"pending_tun_packets"`
		RecentRequests         int                     `json:"recent_requests"`
		RetriesCount           *int                    `json:"retries_count"`
		Retries                json.RawMessage         `json:"retries"`
		Forwarding             FIPSForwardingCounters  `json:"forwarding"`
		Discovery              FIPSDiscoveryCounters   `json:"discovery"`
		ErrorSignals           FIPSErrorSignalCounters `json:"error_signals"`
		Congestion             FIPSCongestionCounters  `json:"congestion"`
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
		Forwarding:             raw.Forwarding,
		Discovery:              raw.Discovery,
		ErrorSignals:           raw.ErrorSignals,
		Congestion:             raw.Congestion,
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
