package main

// dreaming_job.go — background memory-consolidation ("dreaming") job
// (swarmstr-qc53.2).
//
// When enabled via config, a background goroutine runs the light + REM dreaming
// phases on a configurable interval and records a structured dream-diary entry
// per phase. It is OFF by default (opt-in) and mirrors the existing memory
// compaction worker's lifecycle: context-cancellation shutdown, no goroutine
// leaks, config re-read each tick so the interval and enablement can change at
// runtime.
//
// Config lives under cfg.Extra.memory.dreaming:
//
//	{"memory": {"dreaming": {
//	    "enabled": true,
//	    "interval": "6h",     // min 1m
//	    "scope": "",          // diary namespace label
//	    "light_limit": 25,
//	    "rem_limit": 100,
//	    "narratives": true
//	}}}

import (
	"context"
	"log"
	"time"

	nostr "fiatjaf.com/nostr"

	"metiq/internal/memory"
	"metiq/internal/nostr/secure"
	"metiq/internal/store/state"
)

// nip44MemoryPayloadEncryptor adapts the node's NIP-44 self codec to the
// memory.MemoryPayloadEncryptor seam so dream-diary outbox payloads leave the
// node as ciphertext, encrypted to the node's own key.
type nip44MemoryPayloadEncryptor struct {
	codec *secure.NIP44SelfCodec
}

func (e nip44MemoryPayloadEncryptor) EncryptMemoryPayload(plaintext string) (string, string, error) {
	return e.codec.Encrypt(plaintext)
}

// newMemoryPayloadEncryptor builds a NIP-44 self-encryptor from the given keyer,
// or returns nil (leaving outbox payloads in cleartext) when a codec cannot be
// constructed.
func newMemoryPayloadEncryptor(keyer nostr.Keyer) memory.MemoryPayloadEncryptor {
	if keyer == nil {
		return nil
	}
	codec, err := secure.NewNIP44SelfCodec(keyer)
	if err != nil {
		log.Printf("dream diary: NIP-44 payload encryptor unavailable, publishing outbox in cleartext: %v", err)
		return nil
	}
	return nip44MemoryPayloadEncryptor{codec: codec}
}

const (
	defaultDreamingInterval = 6 * time.Hour
	minDreamingInterval     = time.Second
)

type dreamingJobConfig struct {
	Enabled    bool
	Interval   time.Duration
	Scope      string
	LightLimit int
	REMLimit   int
	Narratives bool
}

func dreamingJobConfigFromMemoryExtra(extra map[string]any) dreamingJobConfig {
	cfg := dreamingJobConfig{Interval: defaultDreamingInterval, Narratives: true}
	raw, ok := extra["dreaming"].(map[string]any)
	if !ok {
		return cfg
	}
	cfg.Enabled = boolFromAny(raw["enabled"])
	if d, ok := durationFromAnyLoose(raw["interval"]); ok && d > 0 {
		cfg.Interval = d
	}
	if cfg.Interval < minDreamingInterval {
		cfg.Interval = minDreamingInterval
	}
	if s, ok := raw["scope"].(string); ok {
		cfg.Scope = s
	}
	if n, ok := intFromAnyLoose(raw["light_limit"]); ok && n > 0 {
		cfg.LightLimit = n
	}
	if n, ok := intFromAnyLoose(raw["rem_limit"]); ok && n > 0 {
		cfg.REMLimit = n
	}
	if v, ok := raw["narratives"]; ok {
		cfg.Narratives = boolFromAny(v)
	}
	return cfg
}

// startDreamingJob launches the background dreaming worker. It is a no-op when
// the store cannot persist a dream diary (only the SQLite-backed hybrid store
// can), so a non-supporting backend never spins a doomed goroutine.
func startDreamingJob(ctx context.Context, store memory.Store, currentConfig func() state.ConfigDoc) {
	if store == nil || currentConfig == nil {
		return
	}
	if _, ok := any(store).(memory.DreamDiaryStore); !ok {
		return
	}
	go func() {
		cfg := dreamingJobConfigFromMemoryExtra(memoryExtraConfig(currentConfig()))
		interval := cfg.Interval
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cfg = dreamingJobConfigFromMemoryExtra(memoryExtraConfig(currentConfig()))
				if cfg.Interval != interval {
					interval = cfg.Interval
					ticker.Reset(interval)
				}
				if !cfg.Enabled {
					continue
				}
				runDreamingCycle(ctx, store, cfg)
			}
		}
	}()
}

func runDreamingCycle(ctx context.Context, store memory.Store, cfg dreamingJobConfig) {
	promoCfg := memory.DefaultPromotionConfig()
	dcfg := memory.DreamingConfig{
		Enabled:    true,
		LightLimit: cfg.LightLimit,
		REMLimit:   cfg.REMLimit,
		Narratives: cfg.Narratives,
	}
	result, entries, err := memory.RunMemoryDreamingCycle(ctx, store, promoCfg, dcfg, memory.DreamDiaryWriteOptions{Scope: cfg.Scope})
	if err != nil {
		log.Printf("dreaming job: %v", err)
		return
	}
	promoted := 0
	if result != nil {
		promoted = result.Promoted
	}
	log.Printf("dreaming job ran scope=%q phases=%d promoted=%d diary_entries=%d", cfg.Scope, len(result.Phases), promoted, len(entries))
}

// durationFromAnyLoose parses a duration from a string ("6h") or numeric seconds.
func durationFromAnyLoose(v any) (time.Duration, bool) {
	switch t := v.(type) {
	case string:
		if d, err := time.ParseDuration(t); err == nil {
			return d, true
		}
	case float64:
		return time.Duration(t) * time.Second, true
	case int:
		return time.Duration(t) * time.Second, true
	case int64:
		return time.Duration(t) * time.Second, true
	}
	return 0, false
}

func intFromAnyLoose(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	}
	return 0, false
}
