package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	nostr "fiatjaf.com/nostr"
	"fiatjaf.com/nostr/nip19"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	gatewayws "metiq/internal/gateway/ws"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/store/state"
)

func testOnboardingAdvert() nostruntime.FIPSOverlayAdvert {
	return nostruntime.FIPSOverlayAdvert{
		Identifier: nostruntime.FIPSOverlayAdvertIdentifier,
		Version:    nostruntime.FIPSOverlayAdvertVersion,
		Endpoints: []nostruntime.FIPSOverlayEndpointAdvert{
			{Transport: nostruntime.FIPSOverlayTransportUDP, Addr: "8.8.8.8:2121"},
		},
	}
}

func testOnboardingOptions(t *testing.T) (onboardingServiceOptions, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	dir := t.TempDir()
	var nip65Calls atomic.Int32
	var advertCalls atomic.Int32
	opts := onboardingServiceOptions{
		BootstrapPath: filepath.Join(dir, "bootstrap.json"),
		ConfigPath:    filepath.Join(dir, "config.json"),
		StatePath:     filepath.Join(dir, "onboarding.json"),
		TokenLogger:   func(string) {},
		ProbeRelay: func(_ context.Context, relay string) nostruntime.RelayHealthResult {
			return nostruntime.RelayHealthResult{URL: relay, Reachable: true}
		},
		PublishNIP65: func(context.Context, *nostr.Pool, nostr.Keyer, []string, []string, []string) (string, error) {
			nip65Calls.Add(1)
			return strings.Repeat("1", 64), nil
		},
		PublishAdvert: func(context.Context, *nostr.Pool, nostr.Keyer, []string, nostruntime.FIPSOverlayAdvert) (string, error) {
			advertCalls.Add(1)
			return strings.Repeat("2", 64), nil
		},
	}
	return opts, &nip65Calls, &advertCalls
}

func setupParams(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func callSetup(t *testing.T, h controlRPCHandler, ctx context.Context, method string, params any, mutate func(*nostruntime.ControlRPCInbound)) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	in := nostruntime.ControlRPCInbound{Method: method, Params: setupParams(t, params)}
	if mutate != nil {
		mutate(&in)
	}
	return h.Handle(ctx, in)
}

func TestOnboardingHandlersDurableIdempotentResumeAndSeal(t *testing.T) {
	opts, nip65Calls, advertCalls := testOnboardingOptions(t)
	svc, token, err := openOnboardingService(opts)
	if err != nil {
		t.Fatalf("open onboarding: %v", err)
	}
	if token == "" {
		t.Fatal("first boot did not return setup token")
	}
	stateRaw, err := os.ReadFile(opts.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateRaw), token) {
		t.Fatal("durable onboarding state contains the plaintext setup token")
	}
	h := controlRPCHandler{deps: controlRPCDeps{onboarding: svc}}
	ctx := gatewayws.ContextWithConnectionID(gatewayws.ContextWithLocalConnection(context.Background(), true), "setup-test")

	if _, err := callSetup(t, h, ctx, methods.MethodSetupDetect, methods.SetupTokenRequest{}, nil); err == nil || !strings.Contains(err.Error(), "token") {
		t.Fatalf("setup.detect without token error = %v", err)
	}
	detected, err := callSetup(t, h, ctx, methods.MethodSetupDetect, methods.SetupTokenRequest{SetupToken: token}, nil)
	if err != nil {
		t.Fatalf("setup.detect: %v", err)
	}
	if got := detected.Result.(map[string]any)["phase"]; got != "identity" {
		t.Fatalf("initial phase = %v", got)
	}

	authReq := methods.SetupAuthStartRequest{SetupToken: token, Mode: "generate"}
	auth, err := callSetup(t, h, ctx, methods.MethodSetupAuthStart, authReq, nil)
	if err != nil {
		t.Fatalf("setup.auth.start: %v", err)
	}
	if strings.TrimSpace(auth.Result.(map[string]any)["nsec"].(string)) == "" {
		t.Fatal("generated identity did not return one-time nsec backup")
	}
	authAgain, err := callSetup(t, h, ctx, methods.MethodSetupAuthStart, authReq, nil)
	if err != nil {
		t.Fatalf("idempotent setup.auth.start: %v", err)
	}
	if authAgain.Result.(map[string]any)["resumed"] != true {
		t.Fatalf("second auth result = %#v", authAgain.Result)
	}
	if _, leaked := authAgain.Result.(map[string]any)["nsec"]; leaked {
		t.Fatal("resumed auth leaked generated nsec")
	}

	prepareReq := methods.SetupPrepareStartRequest{
		SetupToken:   token,
		ReadRelays:   []string{"wss://read.example"},
		WriteRelays:  []string{"wss://write.example"},
		WorkspaceDir: filepath.Join(t.TempDir(), "workspace"),
		Providers: state.ProvidersConfig{
			"openai": {Enabled: true, APIKey: "test-secret"},
		},
		CapabilityAdvert: testOnboardingAdvert(),
	}
	prepared, err := callSetup(t, h, ctx, methods.MethodSetupPrepareStart, prepareReq, nil)
	if err != nil {
		t.Fatalf("setup.prepare.start: %v", err)
	}
	preparedAgain, err := callSetup(t, h, ctx, methods.MethodSetupPrepareStart, prepareReq, nil)
	if err != nil {
		t.Fatalf("idempotent setup.prepare.start: %v", err)
	}
	if preparedAgain.Result.(map[string]any)["resumed"] != true || prepared.Result.(map[string]any)["revision"] != preparedAgain.Result.(map[string]any)["revision"] {
		t.Fatalf("prepare results = first %#v second %#v", prepared.Result, preparedAgain.Result)
	}

	// Reopen the service to prove the token verifier and partial state survive restart.
	resumedService, replacementToken, err := openOnboardingService(opts)
	if err != nil {
		t.Fatalf("reopen onboarding: %v", err)
	}
	if replacementToken != "" {
		t.Fatal("restart minted a replacement setup token")
	}
	h.deps.onboarding = resumedService
	if _, err := callSetup(t, h, ctx, methods.MethodSetupDetect, methods.SetupTokenRequest{SetupToken: token}, nil); err != nil {
		t.Fatalf("detect after resume: %v", err)
	}

	verified, err := callSetup(t, h, ctx, methods.MethodSetupVerify, methods.SetupTokenRequest{SetupToken: token}, nil)
	if err != nil {
		t.Fatalf("setup.verify: %v", err)
	}
	if verified.Result.(map[string]any)["ready"] != true {
		t.Fatalf("verification = %#v", verified.Result)
	}
	verifiedAgain, err := callSetup(t, h, ctx, methods.MethodSetupVerify, methods.SetupTokenRequest{SetupToken: token}, nil)
	if err != nil {
		t.Fatalf("idempotent setup.verify: %v", err)
	}
	if verifiedAgain.Result.(map[string]any)["resumed"] != true || nip65Calls.Load() != 1 || advertCalls.Load() != 1 {
		t.Fatalf("verify resume=%#v nip65=%d advert=%d", verifiedAgain.Result, nip65Calls.Load(), advertCalls.Load())
	}

	activated, err := callSetup(t, h, ctx, methods.MethodSetupActivate, methods.SetupTokenRequest{SetupToken: token}, nil)
	if err != nil {
		t.Fatalf("setup.activate: %v", err)
	}
	if activated.Result.(map[string]any)["sealed"] != true {
		t.Fatalf("activation = %#v", activated.Result)
	}
	if _, err := config.LoadBootstrap(opts.BootstrapPath); err != nil {
		t.Fatalf("load committed bootstrap: %v", err)
	}
	live, err := config.LoadConfigFile(opts.ConfigPath)
	if err != nil {
		t.Fatalf("load committed config: %v", err)
	}
	if len(live.Relays.Read) != 1 || len(live.Relays.Write) != 1 || len(live.Providers) != 1 || len(live.Agents) != 1 {
		t.Fatalf("committed config = %#v", live)
	}
	for _, path := range []string{opts.StatePath, opts.BootstrapPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s permissions = %o", path, info.Mode().Perm())
		}
	}
	if _, err := callSetup(t, h, ctx, methods.MethodSetupDetect, methods.SetupTokenRequest{SetupToken: token}, nil); err == nil || !strings.Contains(err.Error(), "sealed") {
		t.Fatalf("post-seal detect error = %v", err)
	}
	sealedService, _, err := openOnboardingService(onboardingServiceOptions{
		BootstrapPath:     opts.BootstrapPath,
		ConfigPath:        opts.ConfigPath,
		StatePath:         opts.StatePath,
		Bootstrap:         mustLoadBootstrap(t, opts.BootstrapPath),
		Config:            live,
		IdentityCommitted: true,
		TokenLogger:       func(string) { t.Fatal("sealed restart printed a setup token") },
	})
	if err != nil || !sealedService.state.Sealed || sealedService.state.TokenHash != "" {
		t.Fatalf("sealed restart service=%#v err=%v", sealedService, err)
	}
}

func TestFirstRunOnboardingListenAddressMustBeLoopback(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8788", true},
		{"[::1]:8788", true},
		{"localhost:8788", true},
		{"0.0.0.0:8788", false},
		{"192.0.2.10:8788", false},
		{"bad-address", false},
	} {
		if got := loopbackListenAddr(tc.addr); got != tc.want {
			t.Errorf("loopbackListenAddr(%q) = %v want %v", tc.addr, got, tc.want)
		}
	}
}

func TestOnboardingExistingUnsealedStateWithoutVerifierFailsClosed(t *testing.T) {
	opts, _, _ := testOnboardingOptions(t)
	if err := os.WriteFile(opts.StatePath, []byte(`{"version":1,"phase":"identity","sealed":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	printed := false
	opts.TokenLogger = func(string) { printed = true }
	if _, _, err := openOnboardingService(opts); err == nil || !strings.Contains(err.Error(), "no token verifier") {
		t.Fatalf("malformed state error = %v", err)
	}
	if printed {
		t.Fatal("malformed existing state minted a replacement token")
	}
}

func TestOnboardingPreIdentityTransportBoundary(t *testing.T) {
	opts, _, _ := testOnboardingOptions(t)
	svc, token, err := openOnboardingService(opts)
	if err != nil {
		t.Fatal(err)
	}
	h := controlRPCHandler{deps: controlRPCDeps{onboarding: svc}}
	params := methods.SetupTokenRequest{SetupToken: token}

	if _, err := callSetup(t, h, context.Background(), methods.MethodSetupDetect, params, nil); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("untrusted transport error = %v", err)
	}
	localOnly := gatewayws.ContextWithLocalConnection(context.Background(), true)
	if _, err := callSetup(t, h, localOnly, methods.MethodSetupDetect, params, nil); err == nil || !strings.Contains(err.Error(), "direct gateway WebSocket") {
		t.Fatalf("missing WebSocket marker error = %v", err)
	}
	local := gatewayws.ContextWithConnectionID(localOnly, "setup-boundary-test")
	for _, mutate := range []func(*nostruntime.ControlRPCInbound){
		func(in *nostruntime.ControlRPCInbound) { in.EventID = strings.Repeat("a", 64) },
		func(in *nostruntime.ControlRPCInbound) { in.RequestID = "nostr-request" },
		func(in *nostruntime.ControlRPCInbound) { in.RelayURL = "wss://relay.example" },
	} {
		if _, err := callSetup(t, h, local, methods.MethodSetupDetect, params, mutate); err == nil || !strings.Contains(err.Error(), "Nostr RPC bus") {
			t.Fatalf("Nostr transport error = %v", err)
		}
	}
	if _, err := callSetup(t, h, local, methods.MethodSetupDetect, params, func(in *nostruntime.ControlRPCInbound) { in.Internal = true }); err == nil || !strings.Contains(err.Error(), "internal redispatch") {
		t.Fatalf("internal redispatch error = %v", err)
	}
	if _, err := callSetup(t, h, local, methods.MethodSetupDetect, methods.SetupTokenRequest{SetupToken: "wrong"}, nil); err == nil || !strings.Contains(err.Error(), "token invalid") {
		t.Fatalf("wrong token error = %v", err)
	}
}

func TestOnboardingAuthStartSupportsGenerateImportAndNIP46(t *testing.T) {
	generated := nostr.Generate()
	nsec := nip19.EncodeNsec([32]byte(generated))
	remote := nostr.Generate()
	remoteHex := hex.EncodeToString(remote[:])

	for _, tc := range []struct {
		name      string
		req       methods.SetupAuthStartRequest
		configure func(*onboardingServiceOptions)
		wantPK    string
	}{
		{name: "generate", req: methods.SetupAuthStartRequest{Mode: "generate"}},
		{name: "import_nsec", req: methods.SetupAuthStartRequest{Mode: "import_nsec", Nsec: nsec}, wantPK: generated.Public().Hex()},
		{
			name:   "nip46",
			req:    methods.SetupAuthStartRequest{Mode: "nip46", SignerURL: "bunker://remote.example"},
			wantPK: remote.Public().Hex(),
			configure: func(opts *onboardingServiceOptions) {
				opts.ResolveSigner = func(ctx context.Context, _ config.BootstrapConfig) (nostr.Keyer, error) {
					return config.ResolveSigner(ctx, config.BootstrapConfig{PrivateKey: remoteHex}, nil)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, _, _ := testOnboardingOptions(t)
			if tc.configure != nil {
				tc.configure(&opts)
			}
			svc, token, err := openOnboardingService(opts)
			if err != nil {
				t.Fatal(err)
			}
			req := tc.req
			req.SetupToken = token
			req, err = req.Normalize()
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			got, err := svc.AuthStart(context.Background(), req)
			if err != nil {
				t.Fatalf("AuthStart: %v", err)
			}
			if tc.wantPK != "" && got["public_key"] != tc.wantPK {
				t.Fatalf("public key = %v want %s", got["public_key"], tc.wantPK)
			}
			if tc.name == "nip46" && svc.state.Identity.NIP46ClientKey == "" {
				t.Fatal("NIP-46 client key was not generated and persisted")
			}
			if tc.name == "import_nsec" {
				other := nostr.Generate()
				conflict := methods.SetupAuthStartRequest{SetupToken: token, Mode: "import_nsec", Nsec: nip19.EncodeNsec([32]byte(other))}
				conflict, err = conflict.Normalize()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := svc.AuthStart(context.Background(), conflict); err == nil || !strings.Contains(err.Error(), "conflicts") {
					t.Fatalf("conflicting import error = %v", err)
				}
			}
		})
	}
}

func mustLoadBootstrap(t *testing.T, path string) config.BootstrapConfig {
	t.Helper()
	got, err := config.LoadBootstrap(path)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
