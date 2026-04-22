package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	nostruntime "metiq/internal/nostr/runtime"
)

const DefaultBootstrapRelPath = ".metiq/bootstrap.json"

func DefaultBootstrapPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DefaultBootstrapRelPath), nil
}

func LoadBootstrap(path string) (BootstrapConfig, error) {
	return loadBootstrap(path, false)
}

func LoadBootstrapForControl(path string) (BootstrapConfig, error) {
	return loadBootstrap(path, true)
}

func loadBootstrap(path string, allowControlSigner bool) (BootstrapConfig, error) {
	if path == "" {
		defaultPath, err := DefaultBootstrapPath()
		if err != nil {
			return BootstrapConfig{}, err
		}
		path = defaultPath
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return BootstrapConfig{}, err
	}

	var cfg BootstrapConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return BootstrapConfig{}, fmt.Errorf("parse bootstrap config: %w", err)
	}
	if len(cfg.Relays) == 0 {
		return BootstrapConfig{}, errors.New("bootstrap config requires at least one relay — " +
			"relays are needed at startup to connect the state store and DM transport before the main config loads; " +
			"add e.g. \"relays\": [\"wss://relay.damus.io\"] to your bootstrap.json")
	}
	if cfg.PrivateKey == "" && cfg.SignerURL == "" && !(allowControlSigner && strings.TrimSpace(cfg.ControlSignerURL) != "") {
		return BootstrapConfig{}, errors.New("bootstrap config requires private_key or signer_url — " +
			"private_key accepts 64-char hex, nsec1... bech32, or $ENV_VAR; " +
			"signer_url accepts bunker://, env://VAR_NAME, or file:///path")
	}
	if target := strings.TrimSpace(cfg.ControlTargetPubKey); target != "" {
		if _, err := nostruntime.ParsePubKey(target); err != nil {
			return BootstrapConfig{}, fmt.Errorf("bootstrap config control_target_pubkey: %w", err)
		}
	}
	return cfg, nil
}
