//go:build experimental_fips

package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFIPSControlV04SnapshotsDecode(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		decode func(json.RawMessage) error
	}{
		{name: "show_status", file: "fips_show_status_v04.json", decode: func(data json.RawMessage) error {
			var out FIPSStatusSummary
			return json.Unmarshal(data, &out)
		}},
		{name: "show_routing", file: "fips_show_routing_v04.json", decode: func(data json.RawMessage) error {
			_, err := parseFIPSRoutingSummary(data)
			return err
		}},
		{name: "show_metrics", file: "fips_show_stats_list_v04.json", decode: func(data json.RawMessage) error {
			var out struct {
				Metrics              []FIPSMetricDescriptor `json:"metrics"`
				FastRingSeconds      int                    `json:"fast_ring_seconds"`
				SlowRingMinutes      int                    `json:"slow_ring_minutes"`
				PeerRetentionSeconds int                    `json:"peer_retention_seconds"`
			}
			return json.Unmarshal(data, &out)
		}},
		{name: "show_bloom", file: "fips_show_bloom_v04.json", decode: func(data json.RawMessage) error {
			var out FIPSBloomSummary
			return json.Unmarshal(data, &out)
		}},
		{name: "show_tree", file: "fips_show_tree_v04.json", decode: func(data json.RawMessage) error {
			var out FIPSTreeSummary
			return json.Unmarshal(data, &out)
		}},
		{name: "show_peers", file: "fips_show_peers_v04.json", decode: func(data json.RawMessage) error {
			var out FIPSPeersSummary
			return json.Unmarshal(data, &out)
		}},
		{name: "show_transports", file: "fips_show_transports_v04.json", decode: func(data json.RawMessage) error {
			var out FIPSTransportsSummary
			return json.Unmarshal(data, &out)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := readFIPSControlSnapshotData(t, tt.file)
			if err := tt.decode(data); err != nil {
				t.Fatalf("decode %s: %v", tt.file, err)
			}
		})
	}
}

func readFIPSControlSnapshotData(t *testing.T, name string) json.RawMessage {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	data, err := parseFIPSControlData(payload)
	if err != nil {
		t.Fatalf("parse fixture envelope %s: %v", name, err)
	}
	return data
}

type FIPSMetricDescriptor struct {
	Name  string `json:"name"`
	Scope string `json:"scope"`
	Unit  string `json:"unit"`
}
