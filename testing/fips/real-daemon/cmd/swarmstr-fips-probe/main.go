//go:build experimental_fips

// swarmstr-fips-probe is an opt-in real-daemon interoperability helper.
// It intentionally lives inside the metiq module so it exercises the production
// FIPS transport and control-channel implementations.
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	nostr "fiatjaf.com/nostr"
	"github.com/coder/websocket"
	runtime "metiq/internal/nostr/runtime"
)

const (
	frameDM         = byte(0x01)
	frameControlReq = byte(0x02)
	frameControlRes = byte(0x03)
	maxFrameBytes   = 256 * 1024
)

type controlRequest struct {
	Version   int             `json:"v"`
	RequestID string          `json:"req_id"`
	From      string          `json:"from"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
}

type controlResponse struct {
	Version   int             `json:"v"`
	RequestID string          `json:"req_id"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	ErrorCode int             `json:"error_code,omitempty"`
}

type dmEnvelope struct {
	Version int    `json:"v"`
	From    string `json:"from"`
	Text    string `json:"text"`
	TS      int64  `json:"ts"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: swarmstr-fips-probe <relay|advert|serve|send|send-raw|control> [flags]")
	}
	switch args[0] {
	case "relay":
		return runRelay(args[1:])
	case "advert":
		return runAdvert(args[1:])
	case "serve":
		return runServe(args[1:])
	case "send":
		return runSend(args[1:])
	case "send-raw":
		return runRawDM(args[1:])
	case "control":
		return runControl(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func parseHexPubkey(value string) (string, error) {
	pk, err := runtime.ParsePubKey(strings.TrimSpace(value))
	if err != nil {
		return "", err
	}
	return pk.Hex(), nil
}

func newTransport(self string, onMessage func(context.Context, runtime.InboundDM) error, onError func(error)) (*runtime.FIPSTransport, error) {
	hexSelf, err := parseHexPubkey(self)
	if err != nil {
		return nil, fmt.Errorf("parse self pubkey: %w", err)
	}
	return runtime.NewFIPSTransport(runtime.FIPSTransportOptions{
		PubkeyHex: hexSelf,
		OnMessage: onMessage,
		OnError:   onError,
	})
}

func prime(ctx context.Context, transport *runtime.FIPSTransport, peer string) (string, error) {
	hexPeer, err := parseHexPubkey(peer)
	if err != nil {
		return "", fmt.Errorf("parse peer pubkey: %w", err)
	}
	if err := transport.PrimeIdentity(ctx, hexPeer); err != nil {
		return "", fmt.Errorf("prime peer identity: %w", err)
	}
	return hexPeer, nil
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	self := fs.String("self", "", "local npub or hex pubkey")
	peer := fs.String("peer", "", "authenticated peer npub or hex pubkey")
	expectText := fs.String("expect-text", "", "expected DM text")
	readyFile := fs.String("ready-file", "", "readiness marker written after listeners bind")
	deadline := fs.Duration("deadline", 60*time.Second, "failure-only completion bound")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *deadline)
	defer cancel()
	hexSelf, err := parseHexPubkey(*self)
	if err != nil {
		return err
	}
	hexPeer, err := parseHexPubkey(*peer)
	if err != nil {
		return err
	}

	dmOK := make(chan struct{}, 1)
	dmRejected := make(chan struct{}, 1)
	controlOK := make(chan struct{}, 1)
	controlRejected := make(chan struct{}, 1)
	notify := func(ch chan struct{}) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}

	transport, err := newTransport(hexSelf, func(_ context.Context, dm runtime.InboundDM) error {
		if dm.FromPubKey != hexPeer || dm.Scheme != "fips" || dm.Text != *expectText {
			return fmt.Errorf("unexpected DM: from=%s scheme=%s text=%q", dm.FromPubKey, dm.Scheme, dm.Text)
		}
		notify(dmOK)
		return nil
	}, func(err error) {
		log.Printf("transport: %v", err)
		if strings.Contains(err.Error(), "does not match authenticated identity") {
			notify(dmRejected)
		}
	})
	if err != nil {
		return err
	}
	defer transport.Close()
	if _, err := prime(ctx, transport, hexPeer); err != nil {
		return err
	}
	if err := transport.Start(); err != nil {
		return err
	}

	control, err := runtime.NewFIPSControlChannel(runtime.FIPSControlChannelOptions{
		PubkeyHex:        hexSelf,
		IdentityResolver: transport.ResolveIdentity,
		OnRequest: func(_ context.Context, request runtime.ControlRPCInbound) (runtime.ControlRPCResult, error) {
			if request.FromPubKey != hexPeer || !request.Authenticated || request.Method != "echo" {
				return runtime.ControlRPCResult{}, fmt.Errorf("unexpected control request: from=%s authenticated=%v method=%s", request.FromPubKey, request.Authenticated, request.Method)
			}
			notify(controlOK)
			return runtime.ControlRPCResult{Result: map[string]any{"from": request.FromPubKey, "ok": true}}, nil
		},
		OnError: func(err error) {
			log.Printf("control: %v", err)
			if strings.Contains(err.Error(), "does not match authenticated identity") {
				notify(controlRejected)
			}
		},
	})
	if err != nil {
		return err
	}
	defer control.Close()
	if err := control.Start(); err != nil {
		return err
	}
	if *readyFile != "" {
		if err := os.WriteFile(*readyFile, []byte("ready\n"), 0o600); err != nil {
			return err
		}
	}
	fmt.Println("READY")

	pending := map[string]<-chan struct{}{
		"authenticated DM":        dmOK,
		"rejected forged DM":      dmRejected,
		"authenticated control":   controlOK,
		"rejected forged control": controlRejected,
	}
	for name, ch := range pending {
		select {
		case <-ch:
			log.Printf("observed %s", name)
		case <-ctx.Done():
			return fmt.Errorf("waiting for %s: %w", name, ctx.Err())
		}
	}
	fmt.Println("AUTHENTICATED_TRANSPORT_OK")
	return nil
}

func runSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	self := fs.String("self", "", "local npub or hex pubkey")
	to := fs.String("to", "", "recipient npub or hex pubkey")
	text := fs.String("text", "", "message text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	transport, err := newTransport(*self, nil, func(err error) { log.Printf("transport: %v", err) })
	if err != nil {
		return err
	}
	defer transport.Close()
	hexTo, err := prime(ctx, transport, *to)
	if err != nil {
		return err
	}
	if err := transport.SendDM(ctx, hexTo, *text); err != nil {
		return err
	}
	fmt.Println("DM_SENT")
	return nil
}

func runRawDM(args []string) error {
	fs := flag.NewFlagSet("send-raw", flag.ContinueOnError)
	self := fs.String("self", "", "local npub or hex pubkey")
	to := fs.String("to", "", "recipient npub or hex pubkey")
	claim := fs.String("claim", "", "claimed sender pubkey")
	text := fs.String("text", "forged", "message text")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	transport, err := newTransport(*self, nil, nil)
	if err != nil {
		return err
	}
	defer transport.Close()
	hexTo, err := prime(ctx, transport, *to)
	if err != nil {
		return err
	}
	hexClaim, err := parseHexPubkey(*claim)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(dmEnvelope{Version: runtime.FIPSApplicationProtocolVersion, From: hexClaim, Text: *text, TS: time.Now().Unix()})
	if err != nil {
		return err
	}
	if _, err := dialAndWrite(ctx, hexTo, runtime.FIPSDefaultAgentPort, frameDM, payload, false); err != nil {
		return err
	}
	fmt.Println("FORGED_DM_SENT")
	return nil
}

func runControl(args []string) error {
	fs := flag.NewFlagSet("control", flag.ContinueOnError)
	self := fs.String("self", "", "local npub or hex pubkey")
	to := fs.String("to", "", "recipient npub or hex pubkey")
	claim := fs.String("claim", "", "claimed sender; defaults to self")
	expectError := fs.Bool("expect-error", false, "require an authentication error response")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	hexSelf, err := parseHexPubkey(*self)
	if err != nil {
		return err
	}
	transport, err := newTransport(hexSelf, nil, nil)
	if err != nil {
		return err
	}
	defer transport.Close()
	hexTo, err := prime(ctx, transport, *to)
	if err != nil {
		return err
	}
	hexClaim := hexSelf
	if *claim != "" {
		hexClaim, err = parseHexPubkey(*claim)
		if err != nil {
			return err
		}
	}
	requestID := fmt.Sprintf("interop-%d", time.Now().UnixNano())
	payload, err := json.Marshal(controlRequest{
		Version: runtime.FIPSApplicationProtocolVersion, RequestID: requestID,
		From: hexClaim, Method: "echo", Params: json.RawMessage(`{"value":"interop"}`),
	})
	if err != nil {
		return err
	}
	responsePayload, err := dialAndWrite(ctx, hexTo, 1338, frameControlReq, payload, true)
	if err != nil {
		return err
	}
	var response controlResponse
	if err := json.Unmarshal(responsePayload, &response); err != nil {
		return err
	}
	if response.Version != runtime.FIPSApplicationProtocolVersion || response.RequestID != requestID {
		return fmt.Errorf("invalid control response: %+v", response)
	}
	if *expectError {
		if response.Error == "" {
			return fmt.Errorf("forged control request unexpectedly succeeded: %s", response.Result)
		}
		fmt.Println("FORGED_CONTROL_REJECTED")
		return nil
	}
	if response.Error != "" {
		return fmt.Errorf("control response error %d: %s", response.ErrorCode, response.Error)
	}
	fmt.Println("CONTROL_OK")
	return nil
}

func dialAndWrite(ctx context.Context, toPubkey string, port int, frameType byte, payload []byte, readResponse bool) ([]byte, error) {
	addr, err := runtime.FIPSAddrString(toPubkey, port)
	if err != nil {
		return nil, err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp6", addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	frame := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(payload)))
	frame[4] = frameType
	copy(frame[5:], payload)
	if _, err := conn.Write(frame); err != nil {
		return nil, err
	}
	if !readResponse {
		return nil, nil
	}
	header := make([]byte, 5)
	if _, err := io.ReadFull(conn, header); err != nil {
		return nil, err
	}
	if header[4] != frameControlRes {
		return nil, fmt.Errorf("unexpected response frame type %d", header[4])
	}
	length := binary.BigEndian.Uint32(header[:4])
	if length > maxFrameBytes {
		return nil, fmt.Errorf("response too large: %d", length)
	}
	response := make([]byte, length)
	_, err = io.ReadFull(conn, response)
	return response, err
}

// relayFilter implements the subset of NIP-01 filters used by FIPS.
type relayFilter struct {
	Kinds   []int
	Authors []string
	Since   *int64
	Until   *int64
	Limit   int
	Tags    map[string][]string
}

func decodeFilter(raw json.RawMessage) (relayFilter, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return relayFilter{}, err
	}
	filter := relayFilter{Tags: make(map[string][]string)}
	for key, value := range values {
		switch key {
		case "kinds":
			_ = json.Unmarshal(value, &filter.Kinds)
		case "authors":
			_ = json.Unmarshal(value, &filter.Authors)
		case "since":
			_ = json.Unmarshal(value, &filter.Since)
		case "until":
			_ = json.Unmarshal(value, &filter.Until)
		case "limit":
			_ = json.Unmarshal(value, &filter.Limit)
		default:
			if strings.HasPrefix(key, "#") {
				var tags []string
				if err := json.Unmarshal(value, &tags); err == nil {
					filter.Tags[strings.TrimPrefix(key, "#")] = tags
				}
			}
		}
	}
	return filter, nil
}

func (filter relayFilter) matches(event nostr.Event) bool {
	if len(filter.Kinds) > 0 {
		matched := false
		for _, kind := range filter.Kinds {
			if int(event.Kind) == kind {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(filter.Authors) > 0 {
		author := event.PubKey.Hex()
		matched := false
		for _, prefix := range filter.Authors {
			if strings.HasPrefix(author, strings.ToLower(prefix)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	created := int64(event.CreatedAt)
	if filter.Since != nil && created < *filter.Since {
		return false
	}
	if filter.Until != nil && created > *filter.Until {
		return false
	}
	for name, wanted := range filter.Tags {
		matched := false
		for _, tag := range event.Tags {
			if len(tag) < 2 || tag[0] != name {
				continue
			}
			for _, value := range wanted {
				if tag[1] == value {
					matched = true
				}
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

type relaySubscription struct {
	id      string
	filters []relayFilter
}

type relayClient struct {
	conn *websocket.Conn
	mu   sync.Mutex
	subs map[string]relaySubscription
}

func (client *relayClient) write(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	return client.conn.Write(ctx, websocket.MessageText, payload)
}

type relayServer struct {
	mu      sync.RWMutex
	events  map[string]nostr.Event
	clients map[*relayClient]struct{}
}

func newRelayServer() *relayServer {
	return &relayServer{events: make(map[string]nostr.Event), clients: make(map[*relayClient]struct{})}
}

func relayEventKey(event nostr.Event) string {
	kind := int(event.Kind)
	if kind >= 30000 && kind < 40000 {
		d := ""
		for _, tag := range event.Tags {
			if len(tag) >= 2 && tag[0] == "d" {
				d = tag[1]
				break
			}
		}
		return fmt.Sprintf("p:%s:%d:%s", event.PubKey.Hex(), kind, d)
	}
	if kind == 0 || kind == 3 || (kind >= 10000 && kind < 20000) {
		return fmt.Sprintf("r:%s:%d", event.PubKey.Hex(), kind)
	}
	return event.ID.Hex()
}

func (server *relayServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	client := &relayClient{conn: conn, subs: make(map[string]relaySubscription)}
	server.mu.Lock()
	server.clients[client] = struct{}{}
	server.mu.Unlock()
	defer func() {
		server.mu.Lock()
		delete(server.clients, client)
		server.mu.Unlock()
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}()

	for {
		_, payload, err := conn.Read(r.Context())
		if err != nil {
			return
		}
		var parts []json.RawMessage
		if json.Unmarshal(payload, &parts) != nil || len(parts) == 0 {
			continue
		}
		var command string
		if json.Unmarshal(parts[0], &command) != nil {
			continue
		}
		switch command {
		case "EVENT":
			if len(parts) != 2 {
				continue
			}
			var event nostr.Event
			if err := json.Unmarshal(parts[1], &event); err != nil || !event.CheckID() || !event.VerifySignature() {
				_ = client.write(r.Context(), []any{"OK", event.ID.Hex(), false, "invalid: event verification failed"})
				continue
			}
			server.storeAndBroadcast(r.Context(), event)
			_ = client.write(r.Context(), []any{"OK", event.ID.Hex(), true, ""})
		case "REQ":
			if len(parts) < 3 {
				continue
			}
			var id string
			if json.Unmarshal(parts[1], &id) != nil {
				continue
			}
			sub := relaySubscription{id: id}
			for _, raw := range parts[2:] {
				filter, err := decodeFilter(raw)
				if err == nil {
					sub.filters = append(sub.filters, filter)
				}
			}
			client.mu.Lock()
			client.subs[id] = sub
			client.mu.Unlock()
			for _, event := range server.snapshot(sub.filters) {
				if err := client.write(r.Context(), []any{"EVENT", id, event}); err != nil {
					return
				}
			}
			if err := client.write(r.Context(), []any{"EOSE", id}); err != nil {
				return
			}
		case "CLOSE":
			if len(parts) != 2 {
				continue
			}
			var id string
			_ = json.Unmarshal(parts[1], &id)
			client.mu.Lock()
			delete(client.subs, id)
			client.mu.Unlock()
		}
	}
}

func (server *relayServer) snapshot(filters []relayFilter) []nostr.Event {
	server.mu.RLock()
	defer server.mu.RUnlock()
	var result []nostr.Event
	for _, event := range server.events {
		for _, filter := range filters {
			if filter.matches(event) {
				result = append(result, event)
				break
			}
		}
	}
	return result
}

func (server *relayServer) storeAndBroadcast(ctx context.Context, event nostr.Event) {
	key := relayEventKey(event)
	server.mu.Lock()
	if previous, ok := server.events[key]; !ok || previous.CreatedAt <= event.CreatedAt {
		server.events[key] = event
	}
	clients := make([]*relayClient, 0, len(server.clients))
	for client := range server.clients {
		clients = append(clients, client)
	}
	server.mu.Unlock()
	for _, client := range clients {
		client.mu.Lock()
		var ids []string
		for id, sub := range client.subs {
			for _, filter := range sub.filters {
				if filter.matches(event) {
					ids = append(ids, id)
					break
				}
			}
		}
		client.mu.Unlock()
		for _, id := range ids {
			_ = client.write(ctx, []any{"EVENT", id, event})
		}
	}
}

func runRelay(args []string) error {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	listen := fs.String("listen", ":7777", "relay listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	log.Printf("Nostr relay listening on %s", *listen)
	return http.ListenAndServe(*listen, newRelayServer())
}

func runAdvert(args []string) error {
	fs := flag.NewFlagSet("advert", flag.ContinueOnError)
	relayURL := fs.String("relay", "ws://127.0.0.1:7777", "relay WebSocket URL")
	authors := fs.String("authors", "", "comma-separated expected npubs or hex pubkeys")
	if err := fs.Parse(args); err != nil {
		return err
	}
	expected := make(map[string]struct{})
	for _, author := range strings.Split(*authors, ",") {
		if strings.TrimSpace(author) == "" {
			continue
		}
		hexAuthor, err := parseHexPubkey(author)
		if err != nil {
			return err
		}
		expected[hexAuthor] = struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, *relayURL, nil)
	if err != nil {
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	request, _ := json.Marshal([]any{"REQ", "advert-check", map[string]any{"kinds": []int{runtime.FIPSOverlayAdvertKind}, "#d": []string{runtime.FIPSOverlayAdvertIdentifier}}})
	if err := conn.Write(ctx, websocket.MessageText, request); err != nil {
		return err
	}
	seen := make(map[string]struct{})
	for {
		_, payload, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var parts []json.RawMessage
		if json.Unmarshal(payload, &parts) != nil || len(parts) < 2 {
			continue
		}
		var command string
		_ = json.Unmarshal(parts[0], &command)
		switch command {
		case "EVENT":
			if len(parts) != 3 {
				continue
			}
			var event nostr.Event
			if err := json.Unmarshal(parts[2], &event); err != nil {
				return err
			}
			if !event.CheckID() || !event.VerifySignature() {
				return fmt.Errorf("advert %s failed NIP-01 verification", event.ID.Hex())
			}
			if _, err := runtime.ParseFIPSAdvertEvent(&event, runtime.FIPSOverlayAdvertIdentifier, time.Now()); err != nil {
				return fmt.Errorf("advert %s failed schema validation: %w", event.ID.Hex(), err)
			}
			if _, wanted := expected[event.PubKey.Hex()]; wanted {
				seen[event.PubKey.Hex()] = struct{}{}
			}
		case "EOSE":
			for author := range expected {
				if _, ok := seen[author]; !ok {
					return fmt.Errorf("EOSE before expected kind-37195 advert from %s", author)
				}
			}
			fmt.Printf("ADVERTS_OK count=%d\n", len(seen))
			return nil
		}
	}
}
