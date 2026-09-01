package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"metiq/internal/config"
	"metiq/internal/gateway/methods"
	gatewayprotocol "metiq/internal/gateway/protocol"
	gatewayws "metiq/internal/gateway/ws"
	"metiq/internal/store/state"
)

var onboardingMethodNames = []string{
	methods.MethodSetupDetect,
	methods.MethodSetupAuthStart,
	methods.MethodSetupPrepareStart,
	methods.MethodSetupVerify,
	methods.MethodSetupActivate,
}

func resolveFirstRunPaths(bootstrapPath, configPath string) (string, string, error) {
	var err error
	if strings.TrimSpace(bootstrapPath) == "" {
		bootstrapPath, err = config.DefaultBootstrapPath()
		if err != nil {
			return "", "", err
		}
	}
	if strings.TrimSpace(configPath) == "" {
		configPath, err = config.DefaultConfigPath()
		if err != nil {
			return "", "", err
		}
	}
	return filepath.Clean(bootstrapPath), filepath.Clean(configPath), nil
}

func loadBootstrapDraft(path string) (config.BootstrapConfig, bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config.BootstrapConfig{}, false, nil
	}
	if err != nil {
		return config.BootstrapConfig{}, false, err
	}
	var draft config.BootstrapConfig
	if err := json.Unmarshal(raw, &draft); err != nil {
		return config.BootstrapConfig{}, true, fmt.Errorf("parse bootstrap config: %w", err)
	}
	return draft, true, nil
}

func loadRuntimeConfigDraft(path string) (state.ConfigDoc, error) {
	cfg, err := config.LoadConfigFile(path)
	if errors.Is(err, os.ErrNotExist) || (err != nil && strings.Contains(err.Error(), "no such file or directory")) {
		return state.ConfigDoc{}, nil
	}
	return cfg, err
}

func loopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return false
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// runFirstRunOnboarding starts a deliberately minimal loopback WebSocket that
// advertises only setup.*. It returns only after setup.activate's response has
// been written and the committed bootstrap can be loaded normally.
func runFirstRunOnboarding(bootstrapPath, configPath, addrOverride, tokenOverride, pathOverride string) (config.BootstrapConfig, error) {
	draft, _, err := loadBootstrapDraft(bootstrapPath)
	if err != nil {
		return config.BootstrapConfig{}, err
	}
	if strings.TrimSpace(draft.PrivateKey) != "" || strings.TrimSpace(draft.SignerURL) != "" {
		return config.BootstrapConfig{}, fmt.Errorf("bootstrap identity exists; refusing ambiguous first-run recovery")
	}
	live, err := loadRuntimeConfigDraft(configPath)
	if err != nil {
		return config.BootstrapConfig{}, fmt.Errorf("load first-run config draft: %w", err)
	}
	addr := strings.TrimSpace(addrOverride)
	if addr == "" {
		addr = strings.TrimSpace(draft.GatewayWSListenAddr)
	}
	if addr == "" {
		addr = "127.0.0.1:8788"
	}
	if !loopbackListenAddr(addr) {
		return config.BootstrapConfig{}, fmt.Errorf("first-run onboarding gateway must bind to loopback, got %q", addr)
	}
	wsPath := strings.TrimSpace(pathOverride)
	if wsPath == "" {
		wsPath = strings.TrimSpace(draft.GatewayWSPath)
	}
	if wsPath == "" {
		wsPath = "/ws"
	}
	gatewayToken := strings.TrimSpace(tokenOverride)
	if gatewayToken == "" {
		gatewayToken = strings.TrimSpace(draft.GatewayWSToken)
	}

	onboarding, _, err := openOnboardingService(onboardingServiceOptions{
		BootstrapPath: bootstrapPath,
		ConfigPath:    configPath,
		Bootstrap:     draft,
		Config:        live,
	})
	if err != nil {
		return config.BootstrapConfig{}, err
	}
	handler := newControlRPCHandler(controlRPCDeps{onboarding: onboarding, configState: newRuntimeConfigStore(state.ConfigDoc{})})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	activated := make(chan struct{})
	var activatedOnce sync.Once
	runtime, err := gatewayws.Start(ctx, gatewayws.RuntimeOptions{
		Addr:              addr,
		Path:              wsPath,
		Token:             gatewayToken,
		Methods:           onboardingMethodNames,
		MethodDescriptors: methods.MethodDescriptors(onboardingMethodNames),
		Version:           version,
		HandleRequest: func(callCtx context.Context, req gatewayprotocol.RequestFrame) (any, *gatewayprotocol.ErrorShape) {
			principal, _ := gatewayws.PrincipalFromContext(callCtx)
			res, callErr := handler.Handle(callCtx, gatewayControlRPCInbound(principal, req))
			if callErr != nil {
				return nil, mapGatewayWSError(callErr)
			}
			return res.Result, nil
		},
		AfterResponse: func(_ context.Context, req gatewayprotocol.RequestFrame, ok bool) {
			if ok && strings.TrimSpace(req.Method) == methods.MethodSetupActivate {
				activatedOnce.Do(func() { close(activated) })
			}
		},
	})
	if err != nil {
		return config.BootstrapConfig{}, fmt.Errorf("start first-run onboarding gateway: %w", err)
	}
	logFirstRunGateway(addr, wsPath)
	select {
	case <-activated:
	case <-ctx.Done():
		return config.BootstrapConfig{}, ctx.Err()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runtime.Shutdown(shutdownCtx); err != nil {
		return config.BootstrapConfig{}, fmt.Errorf("stop first-run onboarding gateway: %w", err)
	}
	cfg, err := config.LoadBootstrap(bootstrapPath)
	if err != nil {
		return config.BootstrapConfig{}, fmt.Errorf("load activated bootstrap: %w", err)
	}
	return cfg, nil
}

func logFirstRunGateway(addr, path string) {
	fmt.Fprintf(os.Stderr, "Metiq first-run onboarding gateway: ws://%s%s\n", addr, path)
	fmt.Fprintln(os.Stderr, "Only native setup.* methods are available until setup.activate succeeds.")
}
