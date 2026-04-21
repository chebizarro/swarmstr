package main

import (
	"testing"
	"time"

	"metiq/internal/store/state"
)

func TestFIPSTransportOptionsFromConfig_DefaultTimeout(t *testing.T) {
	cfg := state.FIPSConfig{Enabled: true}
	opts := fipsTransportOptionsFromConfig(cfg)
	if opts.DialTimeout != 5*time.Second {
		t.Errorf("expected default 5s dial timeout, got %v", opts.DialTimeout)
	}
	if opts.AgentPort != 1337 {
		t.Errorf("expected default agent port 1337, got %d", opts.AgentPort)
	}
}

func TestFIPSTransportOptionsFromConfig_CustomTimeout(t *testing.T) {
	cfg := state.FIPSConfig{
		Enabled:     true,
		ConnTimeout: "10s",
		AgentPort:   9000,
	}
	opts := fipsTransportOptionsFromConfig(cfg)
	if opts.DialTimeout != 10*time.Second {
		t.Errorf("expected 10s dial timeout, got %v", opts.DialTimeout)
	}
	if opts.AgentPort != 9000 {
		t.Errorf("expected agent port 9000, got %d", opts.AgentPort)
	}
}

func TestTransportSelectorOptionsFromConfig_DefaultTTL(t *testing.T) {
	cfg := state.FIPSConfig{Enabled: true}
	opts := transportSelectorOptionsFromConfig(cfg)
	if opts.ReachCacheTTL != 30*time.Second {
		t.Errorf("expected default 30s reach cache TTL, got %v", opts.ReachCacheTTL)
	}
	if opts.Pref != "fips-first" {
		t.Errorf("expected default pref=fips-first, got %q", opts.Pref)
	}
}

func TestTransportSelectorOptionsFromConfig_CustomTTL(t *testing.T) {
	cfg := state.FIPSConfig{
		Enabled:       true,
		ReachCacheTTL: "2m",
		TransportPref: "relay-first",
	}
	opts := transportSelectorOptionsFromConfig(cfg)
	if opts.ReachCacheTTL != 2*time.Minute {
		t.Errorf("expected 2m reach cache TTL, got %v", opts.ReachCacheTTL)
	}
	if opts.Pref != "relay-first" {
		t.Errorf("expected pref=relay-first, got %q", opts.Pref)
	}
}
