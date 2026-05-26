//go:build !experimental_fips

package runtime

import (
	"context"
	"fmt"
	"time"
)

type FIPSControlClientOptions struct {
	ControlSocket string
	DialTimeout   time.Duration
}

type FIPSControlClient struct{}

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

func NewFIPSControlClient(_ FIPSControlClientOptions) (*FIPSControlClient, error) {
	return nil, fmt.Errorf("fips control client: not compiled (build with -tags experimental_fips)")
}

func (c *FIPSControlClient) Endpoint() (network, address string) { return "", "" }

func (c *FIPSControlClient) ShowCache(context.Context) (FIPSCacheSummary, error) {
	return FIPSCacheSummary{}, fmt.Errorf("fips control client: not compiled")
}

func (c *FIPSControlClient) ShowRouting(context.Context) (FIPSRoutingSummary, error) {
	return FIPSRoutingSummary{}, fmt.Errorf("fips control client: not compiled")
}
