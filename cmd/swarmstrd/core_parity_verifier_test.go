package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"swarmstr/internal/autoreply"
	gatewayws "swarmstr/internal/gateway/ws"
	"swarmstr/internal/store/state"
)

type coreMethodDeviationFixture struct {
	AcceptedMissingMethods    []string `json:"accepted_missing_methods"`
	AcceptedAdditionalMethods []string `json:"accepted_additional_methods"`
}

type methodParitySnapshot struct {
	Entries []struct {
		Method         string `json:"method"`
		Status         string `json:"status"`
		SwarmstrMethod string `json:"swarmstr_method"`
	} `json:"entries"`
}

type coreEventDeviationFixture struct {
	AcceptedMissingEvents    []string `json:"accepted_missing_events"`
	AcceptedAdditionalEvents []string `json:"accepted_additional_events"`
}

type sessionShapeFixture struct {
	RequiredFields  []string `json:"required_fields"`
	ForbiddenFields []string `json:"forbidden_fields"`
}

type queueContractsFixture struct {
	Cases []struct {
		Name       string `json:"name"`
		DefaultCap int    `json:"default_cap"`
		ChannelID  string `json:"channel_id"`
		Config     struct {
			Mode      string            `json:"mode"`
			Cap       int               `json:"cap"`
			Drop      string            `json:"drop"`
			ByChannel map[string]string `json:"by_channel"`
		} `json:"config"`
		Session struct {
			QueueMode string `json:"queue_mode"`
			QueueCap  int    `json:"queue_cap"`
			QueueDrop string `json:"queue_drop"`
		} `json:"session"`
		Expected struct {
			Mode       string `json:"mode"`
			Cap        int    `json:"cap"`
			Drop       string `json:"drop"`
			Collect    bool   `json:"collect"`
			Sequential bool   `json:"sequential"`
		} `json:"expected"`
	} `json:"cases"`
}

type resetContractsFixture struct {
	ParseCases []struct {
		Name      string `json:"name"`
		Input     string `json:"input"`
		Trigger   string `json:"trigger"`
		Remainder string `json:"remainder"`
	} `json:"parse_cases"`
	FreshnessPolicyCases []struct {
		Name        string `json:"name"`
		SessionType string `json:"session_type"`
		ChannelID   string `json:"channel_id"`
		Cfg         struct {
			TTLSeconds   int            `json:"ttl_seconds"`
			SessionReset map[string]any `json:"session_reset"`
		} `json:"cfg"`
		Expected struct {
			IdleMinutes int  `json:"idle_minutes"`
			DailyReset  bool `json:"daily_reset"`
		} `json:"expected"`
	} `json:"freshness_policy_cases"`
	RotateCases []struct {
		Name              string `json:"name"`
		UpdatedAgoMinutes int    `json:"updated_ago_minutes"`
		ExpectRotate      bool   `json:"expect_rotate"`
		Policy            struct {
			IdleMinutes int  `json:"idle_minutes"`
			DailyReset  bool `json:"daily_reset"`
		} `json:"policy"`
	} `json:"rotate_cases"`
}

func TestCoreParityVerifier_MethodSurface(t *testing.T) {
	var snap methodParitySnapshot
	loadJSONFixture(t, filepath.Join("..", "..", "internal", "gateway", "methods", "testdata", "parity", "gateway-method-parity.json"), &snap)
	var dev coreMethodDeviationFixture
	loadJSONFixture(t, filepath.Join("testdata", "parity", "core_method_surface_deviations.json"), &dev)

	expected := map[string]struct{}{}
	for _, entry := range snap.Entries {
		if strings.EqualFold(strings.TrimSpace(entry.Status), "implemented") {
			method := strings.TrimSpace(entry.Method)
			if strings.TrimSpace(entry.SwarmstrMethod) != "" {
				method = strings.TrimSpace(entry.SwarmstrMethod)
			}
			if method != "" {
				expected[method] = struct{}{}
			}
		}
	}

	supported := map[string]struct{}{}
	for _, method := range supportedMethods(state.ConfigDoc{}) {
		supported[method] = struct{}{}
	}

	allowedMissing := toSet(dev.AcceptedMissingMethods)
	allowedAdditional := toSet(dev.AcceptedAdditionalMethods)

	var missing []string
	for method := range expected {
		if _, ok := supported[method]; ok {
			continue
		}
		if _, ok := allowedMissing[method]; ok {
			continue
		}
		missing = append(missing, method)
	}
	sort.Strings(missing)

	var additional []string
	for method := range supported {
		if _, ok := expected[method]; ok {
			continue
		}
		if _, ok := allowedAdditional[method]; ok {
			continue
		}
		additional = append(additional, method)
	}
	sort.Strings(additional)

	if len(missing) > 0 || len(additional) > 0 {
		t.Fatalf(
			"core parity method surface drift detected.\nmissing=%v\nadditional=%v\nAction: update parity fixtures or file follow-up bead with discovered-from:swarmstr-wkb.10",
			missing, additional,
		)
	}
}

func TestCoreParityVerifier_EventSurface(t *testing.T) {
	expectedOpenClawEvents := []string{
		"connect.challenge",
		"agent",
		"chat",
		"presence",
		"tick",
		"talk.mode",
		"shutdown",
		"health",
		"heartbeat",
		"cron",
		"node.pair.requested",
		"node.pair.resolved",
		"node.invoke.request",
		"device.pair.requested",
		"device.pair.resolved",
		"voicewake.changed",
		"exec.approval.requested",
		"exec.approval.resolved",
		"update.available",
	}
	var dev coreEventDeviationFixture
	loadJSONFixture(t, filepath.Join("testdata", "parity", "core_event_surface_deviations.json"), &dev)

	expected := toSet(expectedOpenClawEvents)
	supported := toSet(gatewayws.AllPushEvents)
	allowedMissing := toSet(dev.AcceptedMissingEvents)
	allowedAdditional := toSet(dev.AcceptedAdditionalEvents)

	var missing []string
	for event := range expected {
		if _, ok := supported[event]; ok {
			continue
		}
		if _, ok := allowedMissing[event]; ok {
			continue
		}
		missing = append(missing, event)
	}
	sort.Strings(missing)

	var additional []string
	for event := range supported {
		if _, ok := expected[event]; ok {
			continue
		}
		if _, ok := allowedAdditional[event]; ok {
			continue
		}
		additional = append(additional, event)
	}
	sort.Strings(additional)

	if len(missing) > 0 || len(additional) > 0 {
		t.Fatalf("core parity event surface drift detected.\nmissing=%v\nadditional=%v\nAction: update event deviation fixture or file discovered-from:swarmstr-v6w.3", missing, additional)
	}
}

func TestCoreParityVerifier_SessionEntryShape(t *testing.T) {
	var fx sessionShapeFixture
	loadJSONFixture(t, filepath.Join("testdata", "parity", "session_entry_shape.json"), &fx)

	actual := jsonTagSet(reflect.TypeOf(state.SessionEntry{}))

	var missing []string
	for _, field := range fx.RequiredFields {
		if _, ok := actual[field]; !ok {
			missing = append(missing, field)
		}
	}
	sort.Strings(missing)

	var forbidden []string
	for _, field := range fx.ForbiddenFields {
		if _, ok := actual[field]; ok {
			forbidden = append(forbidden, field)
		}
	}
	sort.Strings(forbidden)

	if len(missing) > 0 || len(forbidden) > 0 {
		t.Fatalf(
			"session entry shape drift detected.\nmissing_required=%v\nforbidden_present=%v\nAction: preserve core contract or file follow-up bead discovered-from:swarmstr-wkb.10",
			missing, forbidden,
		)
	}
}

func TestCoreParityVerifier_QueueRouteContracts(t *testing.T) {
	var fx queueContractsFixture
	loadJSONFixture(t, filepath.Join("testdata", "parity", "queue_route_contracts.json"), &fx)
	for _, tc := range fx.Cases {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			cfg := state.ConfigDoc{}
			queueCfg := map[string]any{}
			if tc.Config.Mode != "" {
				queueCfg["mode"] = tc.Config.Mode
			}
			if tc.Config.Cap != 0 {
				queueCfg["cap"] = float64(tc.Config.Cap)
			}
			if tc.Config.Drop != "" {
				queueCfg["drop"] = tc.Config.Drop
			}
			if len(tc.Config.ByChannel) > 0 {
				by := map[string]any{}
				for k, v := range tc.Config.ByChannel {
					by[k] = v
				}
				queueCfg["by_channel"] = by
			}
			if len(queueCfg) > 0 {
				cfg.Extra = map[string]any{
					"messages": map[string]any{
						"queue": queueCfg,
					},
				}
			}

			var sessionEntry *state.SessionEntry
			if tc.Session.QueueMode != "" || tc.Session.QueueCap != 0 || tc.Session.QueueDrop != "" {
				sessionEntry = &state.SessionEntry{
					QueueMode: tc.Session.QueueMode,
					QueueCap:  tc.Session.QueueCap,
					QueueDrop: tc.Session.QueueDrop,
				}
			}

			got := resolveQueueRuntimeSettings(cfg, sessionEntry, tc.ChannelID, tc.DefaultCap)
			gotDrop := queueDropPolicyName(got.Drop)
			if got.Mode != tc.Expected.Mode || got.Cap != tc.Expected.Cap || gotDrop != tc.Expected.Drop {
				t.Fatalf("queue resolve mismatch got={mode:%s cap:%d drop:%s} want={mode:%s cap:%d drop:%s}",
					got.Mode, got.Cap, gotDrop, tc.Expected.Mode, tc.Expected.Cap, tc.Expected.Drop)
			}
			if queueModeCollect(got.Mode) != tc.Expected.Collect {
				t.Fatalf("collect route mismatch for mode=%s", got.Mode)
			}
			if queueModeSequential(got.Mode) != tc.Expected.Sequential {
				t.Fatalf("sequential route mismatch for mode=%s", got.Mode)
			}
		})
	}
}

func TestCoreParityVerifier_ResetRouteContracts(t *testing.T) {
	var fx resetContractsFixture
	loadJSONFixture(t, filepath.Join("testdata", "parity", "reset_route_contracts.json"), &fx)

	for _, tc := range fx.ParseCases {
		tc := tc
		t.Run("parse/"+tc.Name, func(t *testing.T) {
			trigger, remainder := parseResetTrigger(tc.Input)
			if trigger != tc.Trigger || remainder != tc.Remainder {
				t.Fatalf("parse mismatch got=(%q,%q) want=(%q,%q)", trigger, remainder, tc.Trigger, tc.Remainder)
			}
		})
	}

	for _, tc := range fx.FreshnessPolicyCases {
		tc := tc
		t.Run("policy/"+tc.Name, func(t *testing.T) {
			cfg := state.ConfigDoc{
				Session: state.SessionConfig{TTLSeconds: tc.Cfg.TTLSeconds},
			}
			if len(tc.Cfg.SessionReset) > 0 {
				cfg.Extra = map[string]any{"session_reset": tc.Cfg.SessionReset}
			}
			got := resolveSessionFreshnessPolicy(cfg, tc.SessionType, tc.ChannelID)
			if got.IdleMinutes != tc.Expected.IdleMinutes || got.DailyReset != tc.Expected.DailyReset {
				t.Fatalf("freshness policy mismatch got=%+v want={IdleMinutes:%d DailyReset:%t}",
					got, tc.Expected.IdleMinutes, tc.Expected.DailyReset)
			}
		})
	}

	now := time.Date(2026, 3, 13, 12, 0, 0, 0, time.Local)
	for _, tc := range fx.RotateCases {
		tc := tc
		t.Run("rotate/"+tc.Name, func(t *testing.T) {
			entry := state.SessionEntry{UpdatedAt: now.Add(-time.Duration(tc.UpdatedAgoMinutes) * time.Minute)}
			policy := sessionFreshnessPolicy{IdleMinutes: tc.Policy.IdleMinutes, DailyReset: tc.Policy.DailyReset}
			got := shouldAutoRotateSession(entry, now, policy)
			if got != tc.ExpectRotate {
				t.Fatalf("rotate mismatch got=%t want=%t", got, tc.ExpectRotate)
			}
		})
	}
}

func loadJSONFixture(t *testing.T, path string, out any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
}

func toSet(items []string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out[item] = struct{}{}
	}
	return out
}

func jsonTagSet(typ reflect.Type) map[string]struct{} {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		panic(fmt.Sprintf("jsonTagSet requires struct type, got %s", typ.Kind()))
	}
	out := map[string]struct{}{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = struct{}{}
	}
	return out
}

func queueDropPolicyName(policy autoreply.QueueDropPolicy) string {
	switch policy {
	case autoreply.QueueDropOldest:
		return "oldest"
	case autoreply.QueueDropNewest:
		return "newest"
	default:
		return "summarize"
	}
}

