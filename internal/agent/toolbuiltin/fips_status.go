package toolbuiltin

import (
	"context"
	"encoding/json"
)

// FIPSTransportHealth describes the health of the FIPS DM transport layer.
type FIPSTransportHealth struct {
	Listening         bool   `json:"listening"`
	ListenAddr        string `json:"listen_addr,omitempty"`
	ActiveConnections int    `json:"active_connections"`
	IdentityCacheSize int    `json:"identity_cache_size"`
}

// FIPSControlHealth describes the health of the FIPS control channel.
type FIPSControlHealth struct {
	Listening  bool   `json:"listening"`
	ListenAddr string `json:"listen_addr,omitempty"`
}

// FIPSSelectorHealth describes the state of the transport selector.
type FIPSSelectorHealth struct {
	Preference            string `json:"preference"`
	ReachabilityCacheSize int    `json:"reachability_cache_size"`
}

// FIPSPeerHealth describes the FIPS status of a fleet peer.
type FIPSPeerHealth struct {
	Name      string `json:"name"`
	Pubkey    string `json:"pubkey"`
	FIPSAddr  string `json:"fips_addr,omitempty"`
	Reachable string `json:"reachable"` // "yes", "no", "unknown"
}

// FIPSObservability exposes daemon-owned FIPS v0.4.0 read-surface data when
// the sidecar control socket provides it. Providers may leave fields nil when
// connected to older daemons.
type FIPSObservability struct {
	Metrics             map[string]map[string]int64 `json:"metrics,omitempty"`
	RejectCounters      map[string]map[string]int64 `json:"reject_counters,omitempty"`
	Forwarding          map[string]int64            `json:"forwarding,omitempty"`
	Discovery           map[string]int64            `json:"discovery,omitempty"`
	ErrorSignals        map[string]int64            `json:"error_signals,omitempty"`
	Congestion          map[string]int64            `json:"congestion,omitempty"`
	RouteClassTransit   map[string]int64            `json:"route_class_transit,omitempty"`
	Tree                *FIPSTreeObservability      `json:"tree,omitempty"`
	Bloom               *FIPSBloomObservability     `json:"bloom,omitempty"`
	Transports          []FIPSDaemonTransport       `json:"transports,omitempty"`
	TransportPeerCounts map[string]int              `json:"transport_peer_counts,omitempty"`
	NymDiscovery        *FIPSDiscoveryStatus        `json:"nym_discovery,omitempty"`
	LANDiscovery        *FIPSDiscoveryStatus        `json:"lan_discovery,omitempty"`
}

type FIPSTreeObservability struct {
	Root              string   `json:"root,omitempty"`
	RootNpub          string   `json:"root_npub,omitempty"`
	IsRoot            bool     `json:"is_root"`
	Depth             int      `json:"depth"`
	MyCoords          []string `json:"my_coords,omitempty"`
	Parent            string   `json:"parent,omitempty"`
	PeerTreeCount     int      `json:"peer_tree_count"`
	DeclarationSigned bool     `json:"declaration_signed"`
}

type FIPSBloomObservability struct {
	IsLeafOnly           bool     `json:"is_leaf_only"`
	LeafDependentCount   int      `json:"leaf_dependent_count"`
	LeafDependents       []string `json:"leaf_dependents,omitempty"`
	UptreeFillRatio      *float64 `json:"uptree_fill_ratio,omitempty"`
	UptreeEstimatedCount *int64   `json:"uptree_estimated_count,omitempty"`
}

type FIPSDaemonTransport struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	State     string `json:"state,omitempty"`
	Name      string `json:"name,omitempty"`
	LocalAddr string `json:"local_addr,omitempty"`
}

type FIPSDiscoveryStatus struct {
	Enabled bool   `json:"enabled"`
	State   string `json:"state,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// FIPSStatusResult is the full fips_status tool output.
type FIPSStatusResult struct {
	Enabled       bool                 `json:"enabled"`
	Transport     *FIPSTransportHealth `json:"transport,omitempty"`
	Control       *FIPSControlHealth   `json:"control,omitempty"`
	Selector      *FIPSSelectorHealth  `json:"selector,omitempty"`
	Peers         []FIPSPeerHealth     `json:"peers,omitempty"`
	PeerCount     int                  `json:"peer_count"`
	Observability *FIPSObservability   `json:"observability,omitempty"`
}

// FIPSStatusOpts provides the dependency-injected health providers for the
// fips_status tool. Each field is a function so the tool can query live state.
// Nil functions indicate that the corresponding component is not available.
type FIPSStatusOpts struct {
	// Transport returns FIPS DM transport health. Nil if transport not started.
	Transport func() *FIPSTransportHealth
	// Control returns FIPS control channel health. Nil if not started.
	Control func() *FIPSControlHealth
	// Selector returns transport selector health. Nil if not configured.
	Selector func() *FIPSSelectorHealth
	// Peers returns FIPS-enabled fleet peers with reachability status.
	Peers func() []FIPSPeerHealth
	// Observability returns optional FIPS daemon v0.4.0 control-socket read-surface data.
	Observability func(context.Context) *FIPSObservability
}

// FIPSStatusTool returns a tool that reports FIPS mesh connectivity status.
func FIPSStatusTool(opts FIPSStatusOpts) func(context.Context, map[string]any) (string, error) {
	return func(ctx context.Context, _ map[string]any) (string, error) {
		result := FIPSStatusResult{
			Enabled: opts.Transport != nil || opts.Control != nil || opts.Selector != nil || opts.Observability != nil,
		}

		if opts.Transport != nil {
			result.Transport = opts.Transport()
		}
		if opts.Control != nil {
			result.Control = opts.Control()
		}
		if opts.Selector != nil {
			result.Selector = opts.Selector()
		}
		if opts.Peers != nil {
			result.Peers = opts.Peers()
			result.PeerCount = len(result.Peers)
		}
		if opts.Observability != nil {
			result.Observability = opts.Observability(ctx)
		}

		out, err := json.Marshal(result)
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
}

// FIPSHealthInfo is the JSON-friendly summary of FIPS mesh health,
// suitable for inclusion in the status.get response.
type FIPSHealthInfo struct {
	Enabled            bool   `json:"enabled"`
	TransportListening bool   `json:"transport_listening"`
	TransportAddr      string `json:"transport_addr,omitempty"`
	ControlListening   bool   `json:"control_listening"`
	ControlAddr        string `json:"control_addr,omitempty"`
	ActiveConnections  int    `json:"active_connections"`
	Preference         string `json:"preference,omitempty"`
	FIPSPeerCount      int    `json:"fips_peer_count"`
}

// BuildFIPSHealthInfo constructs FIPSHealthInfo from the same providers used
// by the fips_status tool. Returns nil if FIPS is not enabled.
func BuildFIPSHealthInfo(opts FIPSStatusOpts) *FIPSHealthInfo {
	if opts.Transport == nil && opts.Control == nil && opts.Selector == nil && opts.Observability == nil {
		return nil
	}

	info := &FIPSHealthInfo{Enabled: true}

	if opts.Transport != nil {
		th := opts.Transport()
		if th != nil {
			info.TransportListening = th.Listening
			info.TransportAddr = th.ListenAddr
			info.ActiveConnections = th.ActiveConnections
		}
	}
	if opts.Control != nil {
		ch := opts.Control()
		if ch != nil {
			info.ControlListening = ch.Listening
			info.ControlAddr = ch.ListenAddr
		}
	}
	if opts.Selector != nil {
		sh := opts.Selector()
		if sh != nil {
			info.Preference = sh.Preference
		}
	}
	if opts.Peers != nil {
		info.FIPSPeerCount = len(opts.Peers())
	}

	return info
}
