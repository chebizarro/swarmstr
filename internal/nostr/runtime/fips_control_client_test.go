//go:build experimental_fips

package runtime

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestParseFIPSControlEndpoint(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantNetwork string
		wantAddress string
	}{
		{name: "unix path", input: "/run/fips/control.sock", wantNetwork: "unix", wantAddress: "/run/fips/control.sock"},
		{name: "unix scheme", input: "unix:///tmp/fips-control.sock", wantNetwork: "unix", wantAddress: "/tmp/fips-control.sock"},
		{name: "tcp host port", input: "127.0.0.1:21210", wantNetwork: "tcp", wantAddress: "127.0.0.1:21210"},
		{name: "tcp scheme", input: "tcp://127.0.0.1:21210", wantNetwork: "tcp", wantAddress: "127.0.0.1:21210"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFIPSControlEndpoint(tt.input)
			if err != nil {
				t.Fatalf("parseFIPSControlEndpoint: %v", err)
			}
			if got.Network != tt.wantNetwork || got.Address != tt.wantAddress {
				t.Fatalf("endpoint = %#v, want network=%q address=%q", got, tt.wantNetwork, tt.wantAddress)
			}
		})
	}
}

func TestDefaultFIPSControlEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		got := defaultFIPSControlEndpoint()
		if got.Network != "tcp" || got.Address != defaultFIPSDaemonControlTCP {
			t.Fatalf("windows default = %#v", got)
		}
		return
	}

	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	got := defaultFIPSControlEndpoint()
	if dirExists("/run/fips") {
		if got.Network != "unix" || got.Address != "/run/fips/control.sock" {
			t.Fatalf("/run/fips default = %#v", got)
		}
		return
	}
	if got.Network != "unix" || got.Address != tmp+"/fips/control.sock" {
		t.Fatalf("xdg default = %#v, want unix %s/fips/control.sock", got, tmp)
	}
}

func TestParseFIPSCacheSummaryNewShape(t *testing.T) {
	data := json.RawMessage(`{
		"count":1,
		"max_entries":50000,
		"fill_ratio":0.25,
		"default_ttl_ms":300000,
		"expired":2,
		"avg_age_ms":42,
		"entries":[{
			"node_addr":"abc",
			"display_name":"alice",
			"ipv6_addr":"fd00::1",
			"depth":1,
			"coords":["abc"],
			"age_ms":10,
			"last_used_ms":5,
			"path_mtu":1280
		}]
	}`)
	got, err := parseFIPSCacheSummary(data)
	if err != nil {
		t.Fatalf("parseFIPSCacheSummary: %v", err)
	}
	if got.Count != 1 || got.MaxEntries != 50000 || len(got.Entries) != 1 {
		t.Fatalf("summary = %#v", got)
	}
	if got.Entries[0].NodeAddr != "abc" || got.Entries[0].PathMTU != 1280 {
		t.Fatalf("entry = %#v", got.Entries[0])
	}
}

func TestParseFIPSCacheSummaryOldShapes(t *testing.T) {
	cases := []struct {
		name      string
		data      json.RawMessage
		wantCount int
		wantLen   int
	}{
		{name: "scalar count entries", data: json.RawMessage(`{"entries":2}`), wantCount: 2, wantLen: 0},
		{name: "keyed entries", data: json.RawMessage(`{"entries":{"abc":{"node_addr":"abc","ipv6_addr":"fd00::1"}}}`), wantCount: 1, wantLen: 1},
		{name: "single entry", data: json.RawMessage(`{"entries":{"node_addr":"abc","ipv6_addr":"fd00::1"}}`), wantCount: 1, wantLen: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseFIPSCacheSummary(tc.data)
			if err != nil {
				t.Fatalf("parseFIPSCacheSummary: %v", err)
			}
			if got.Count != tc.wantCount || len(got.Entries) != tc.wantLen {
				t.Fatalf("summary = %#v, want count=%d len=%d", got, tc.wantCount, tc.wantLen)
			}
		})
	}
}

func TestParseFIPSRoutingSummaryNewAndOldShapes(t *testing.T) {
	newData := json.RawMessage(`{
		"coord_cache_entries":3,
		"identity_cache_entries":4,
		"pending_lookups":[{"target":"abc","display_name":"alice","initiated_ms":1,"last_sent_ms":2,"attempt":3,"age_ms":4}],
		"pending_tun_destinations":5,
		"pending_tun_packets":6,
		"recent_requests":7,
		"retries":[{"node_addr":"def","display_name":"bob","retry_count":2,"retry_after_ms":100,"auto_reconnect":true}]
	}`)
	got, err := parseFIPSRoutingSummary(newData)
	if err != nil {
		t.Fatalf("parseFIPSRoutingSummary new: %v", err)
	}
	if got.PendingLookupsCount != 1 || got.RetriesCount != 1 || len(got.PendingLookups) != 1 || len(got.Retries) != 1 {
		t.Fatalf("new summary = %#v", got)
	}

	oldData := json.RawMessage(`{"pending_lookups":2,"retries":{"def":{"node_addr":"def","retry_count":1}}}`)
	got, err = parseFIPSRoutingSummary(oldData)
	if err != nil {
		t.Fatalf("parseFIPSRoutingSummary old: %v", err)
	}
	if got.PendingLookupsCount != 2 || got.RetriesCount != 1 || len(got.Retries) != 1 {
		t.Fatalf("old summary = %#v", got)
	}
}

func TestParseFIPSControlDataEnvelope(t *testing.T) {
	data, err := parseFIPSControlData([]byte(`{"status":"ok","data":{"count":0}}`))
	if err != nil {
		t.Fatalf("parse ok: %v", err)
	}
	if strings.TrimSpace(string(data)) != `{"count":0}` {
		t.Fatalf("data = %s", data)
	}
	if _, err := parseFIPSControlData([]byte(`{"status":"error","message":"nope"}`)); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error response err = %v", err)
	}
}

func TestFIPSControlClientStatusAndProbeTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	serverErr := make(chan error, 1)
	go func() {
		for i := 0; i < 4; i++ {
			conn, err := ln.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			var req struct {
				Command string         `json:"command"`
				Params  map[string]any `json:"params"`
			}
			err = json.NewDecoder(bufio.NewReader(conn)).Decode(&req)
			if err == nil {
				switch req.Command {
				case "show_status":
					_, err = conn.Write([]byte("{\"status\":\"ok\",\"data\":{\"state\":\"Running\"}}\n"))
				case "probe_start":
					_, err = conn.Write([]byte("{\"status\":\"ok\",\"data\":{\"probe_id\":7,\"npub\":\"npub1peer\",\"budget_ms\":5000}}\n"))
				case "probe_poll":
					_, err = conn.Write([]byte("{\"status\":\"ok\",\"data\":{\"state\":\"done\",\"report\":{\"overall\":\"ok\"}}}\n"))
				case "probe_cancel":
					_, err = conn.Write([]byte("{\"status\":\"ok\",\"data\":{}}\n"))
				}
			}
			_ = conn.Close()
			if err != nil {
				serverErr <- err
				return
			}
		}
		serverErr <- nil
	}()

	client, err := NewFIPSControlClient(FIPSControlClientOptions{ControlSocket: ln.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	state, err := client.DaemonState(ctx)
	if err != nil || state != "Running" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	started, err := client.StartProbe(ctx, "npub1peer")
	if err != nil || started.ProbeID != 7 {
		t.Fatalf("start=%#v err=%v", started, err)
	}
	poll, err := client.PollProbe(ctx, started.ProbeID)
	if err != nil || poll.State != "done" || !strings.Contains(string(poll.Report), `"overall":"ok"`) {
		t.Fatalf("poll=%#v err=%v", poll, err)
	}
	if err := client.CancelProbe(ctx, started.ProbeID); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func TestFIPSControlClientShowCacheTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		_, _ = conn.Write([]byte(`{"status":"ok","data":{"count":1,"entries":[{"node_addr":"abc"}]}}` + "\n"))
	}()

	client, err := NewFIPSControlClient(FIPSControlClientOptions{ControlSocket: "tcp://" + ln.Addr().String(), DialTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewFIPSControlClient: %v", err)
	}
	got, err := client.ShowCache(context.Background())
	if err != nil {
		t.Fatalf("ShowCache: %v", err)
	}
	if got.Count != 1 || len(got.Entries) != 1 || got.Entries[0].NodeAddr != "abc" {
		t.Fatalf("summary = %#v", got)
	}
	<-done
}
