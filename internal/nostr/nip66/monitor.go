package nip66

import (
	"context"
	"encoding/hex"
	"encoding/json"
	nostr "fiatjaf.com/nostr"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	KindRelayDiscovery      nostr.Kind = 30166
	KindMonitorAnnouncement nostr.Kind = 10166
)

type RelayStatus struct {
	Relay                                    string
	RTTOpen, RTTRead, RTTWrite               time.Duration
	Network                                  string
	Types, NIPs, Requirements, Topics, Kinds []string
	Geohash                                  string
	NIP11                                    json.RawMessage
	Monitor                                  nostr.PubKey
	CreatedAt                                nostr.Timestamp
	Event                                    nostr.Event
}
type MonitorAnnouncement struct {
	Frequency time.Duration
	Timeouts  map[string]time.Duration
	Checks    []string
	Geohash   string
	Event     nostr.Event
}
type ConsensusStatus struct {
	Relay                               string
	Monitors                            int
	MedianOpen, MedianRead, MedianWrite time.Duration
	Latest                              nostr.Timestamp
}

func NormalizeRelay(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if b, err := hex.DecodeString(raw); err == nil && len(b) == 32 {
		return strings.ToLower(raw), nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "wss" && u.Scheme != "ws") || u.Host == "" {
		return "", fmt.Errorf("invalid relay identifier")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (u.Scheme == "wss" && port == "443") || (u.Scheme == "ws" && port == "80") {
		port = ""
	}
	u.Host = host
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String(), nil
}
func BuildRelayStatus(ctx context.Context, keyer nostr.Signer, status RelayStatus) (nostr.Event, error) {
	if keyer == nil {
		return nostr.Event{}, fmt.Errorf("signer required")
	}
	relay, err := NormalizeRelay(status.Relay)
	if err != nil {
		return nostr.Event{}, err
	}
	tags := nostr.Tags{{"d", relay}}
	addDuration := func(name string, d time.Duration) {
		if d > 0 {
			tags = append(tags, nostr.Tag{name, strconv.FormatInt(d.Milliseconds(), 10)})
		}
	}
	addDuration("rtt-open", status.RTTOpen)
	addDuration("rtt-read", status.RTTRead)
	addDuration("rtt-write", status.RTTWrite)
	if status.Network != "" {
		tags = append(tags, nostr.Tag{"n", status.Network})
	}
	for name, vals := range map[string][]string{"T": status.Types, "N": status.NIPs, "R": status.Requirements, "t": status.Topics, "k": status.Kinds} {
		for _, v := range vals {
			tags = append(tags, nostr.Tag{name, v})
		}
	}
	if status.Geohash != "" {
		tags = append(tags, nostr.Tag{"g", status.Geohash})
	}
	content := string(status.NIP11)
	if content != "" && !json.Valid(status.NIP11) {
		return nostr.Event{}, fmt.Errorf("invalid NIP-11 JSON")
	}
	e := nostr.Event{Kind: KindRelayDiscovery, CreatedAt: nostr.Now(), Tags: tags, Content: content}
	if err := keyer.SignEvent(ctx, &e); err != nil {
		return nostr.Event{}, err
	}
	return e, nil
}
func ParseRelayStatus(e nostr.Event) (RelayStatus, error) {
	if e.Kind != KindRelayDiscovery || !e.CheckID() || !e.VerifySignature() {
		return RelayStatus{}, fmt.Errorf("invalid NIP-66 relay event")
	}
	relay, err := NormalizeRelay(first(e.Tags, "d"))
	if err != nil {
		return RelayStatus{}, err
	}
	s := RelayStatus{Relay: relay, Network: first(e.Tags, "n"), Geohash: first(e.Tags, "g"), Monitor: e.PubKey, CreatedAt: e.CreatedAt, Event: e}
	for _, x := range []struct {
		name string
		dst  *time.Duration
	}{{"rtt-open", &s.RTTOpen}, {"rtt-read", &s.RTTRead}, {"rtt-write", &s.RTTWrite}} {
		if raw := first(e.Tags, x.name); raw != "" {
			ms, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || ms < 0 {
				return RelayStatus{}, fmt.Errorf("invalid %s", x.name)
			}
			*x.dst = time.Duration(ms) * time.Millisecond
		}
	}
	for _, t := range e.Tags {
		if len(t) != 2 {
			continue
		}
		switch t[0] {
		case "T":
			s.Types = append(s.Types, t[1])
		case "N":
			s.NIPs = append(s.NIPs, t[1])
		case "R":
			s.Requirements = append(s.Requirements, t[1])
		case "t":
			s.Topics = append(s.Topics, t[1])
		case "k":
			s.Kinds = append(s.Kinds, t[1])
		}
	}
	if e.Content != "" {
		if !json.Valid([]byte(e.Content)) {
			return RelayStatus{}, fmt.Errorf("invalid NIP-11 content")
		}
		s.NIP11 = json.RawMessage(e.Content)
	}
	return s, nil
}
func BuildMonitorAnnouncement(ctx context.Context, keyer nostr.Signer, frequency time.Duration, timeouts map[string]time.Duration, checks []string, geohash string) (nostr.Event, error) {
	if keyer == nil || frequency <= 0 {
		return nostr.Event{}, fmt.Errorf("frequency and signer required")
	}
	tags := nostr.Tags{{"frequency", strconv.FormatInt(int64(frequency/time.Second), 10)}}
	names := make([]string, 0, len(timeouts))
	for n := range timeouts {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if timeouts[n] <= 0 {
			return nostr.Event{}, fmt.Errorf("invalid timeout")
		}
		tags = append(tags, nostr.Tag{"timeout", n, strconv.FormatInt(timeouts[n].Milliseconds(), 10)})
	}
	for _, c := range checks {
		c = strings.ToLower(c)
		if c == "" {
			return nostr.Event{}, fmt.Errorf("empty check")
		}
		tags = append(tags, nostr.Tag{"c", c})
	}
	if geohash != "" {
		tags = append(tags, nostr.Tag{"g", geohash})
	}
	e := nostr.Event{Kind: KindMonitorAnnouncement, CreatedAt: nostr.Now(), Tags: tags}
	if err := keyer.SignEvent(ctx, &e); err != nil {
		return nostr.Event{}, err
	}
	return e, nil
}
func ParseMonitorAnnouncement(e nostr.Event) (MonitorAnnouncement, error) {
	if e.Kind != KindMonitorAnnouncement || !e.CheckID() || !e.VerifySignature() {
		return MonitorAnnouncement{}, fmt.Errorf("invalid monitor announcement")
	}
	sec, err := strconv.ParseInt(first(e.Tags, "frequency"), 10, 64)
	if err != nil || sec <= 0 {
		return MonitorAnnouncement{}, fmt.Errorf("invalid frequency")
	}
	a := MonitorAnnouncement{Frequency: time.Duration(sec) * time.Second, Timeouts: map[string]time.Duration{}, Geohash: first(e.Tags, "g"), Event: e}
	for _, t := range e.Tags {
		if len(t) >= 2 && t[0] == "c" {
			if t[1] != strings.ToLower(t[1]) {
				return a, fmt.Errorf("checks must be lowercase")
			}
			a.Checks = append(a.Checks, t[1])
		}
		if len(t) == 3 && t[0] == "timeout" {
			ms, err := strconv.ParseInt(t[2], 10, 64)
			if err != nil || ms <= 0 {
				return a, fmt.Errorf("invalid timeout")
			}
			a.Timeouts[t[1]] = time.Duration(ms) * time.Millisecond
		}
	}
	return a, nil
}
func Consensus(events []nostr.Event, trusted map[nostr.PubKey]struct{}, minimum int) map[string]ConsensusStatus {
	if minimum < 2 {
		minimum = 2
	}
	latest := map[string]RelayStatus{}
	for _, e := range events {
		if len(trusted) > 0 {
			if _, ok := trusted[e.PubKey]; !ok {
				continue
			}
		}
		s, err := ParseRelayStatus(e)
		if err != nil {
			continue
		}
		key := e.PubKey.Hex() + "|" + s.Relay
		if old, ok := latest[key]; !ok || s.CreatedAt > old.CreatedAt {
			latest[key] = s
		}
	}
	group := map[string][]RelayStatus{}
	for _, s := range latest {
		group[s.Relay] = append(group[s.Relay], s)
	}
	out := map[string]ConsensusStatus{}
	for relay, ss := range group {
		if len(ss) < minimum {
			continue
		}
		open, read, write := durations(ss, func(s RelayStatus) time.Duration { return s.RTTOpen }), durations(ss, func(s RelayStatus) time.Duration { return s.RTTRead }), durations(ss, func(s RelayStatus) time.Duration { return s.RTTWrite })
		c := ConsensusStatus{Relay: relay, Monitors: len(ss), MedianOpen: median(open), MedianRead: median(read), MedianWrite: median(write)}
		for _, s := range ss {
			if s.CreatedAt > c.Latest {
				c.Latest = s.CreatedAt
			}
		}
		out[relay] = c
	}
	return out
}
func Filter(monitors []nostr.PubKey, since nostr.Timestamp) nostr.Filter {
	return nostr.Filter{Kinds: []nostr.Kind{KindRelayDiscovery}, Authors: monitors, Since: since}
}
func first(tags nostr.Tags, n string) string {
	for _, t := range tags {
		if len(t) >= 2 && t[0] == n {
			return t[1]
		}
	}
	return ""
}
func durations(ss []RelayStatus, f func(RelayStatus) time.Duration) []time.Duration {
	r := []time.Duration{}
	for _, s := range ss {
		if d := f(s); d > 0 {
			r = append(r, d)
		}
	}
	return r
}
func median(v []time.Duration) time.Duration {
	if len(v) == 0 {
		return 0
	}
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	return v[len(v)/2]
}
