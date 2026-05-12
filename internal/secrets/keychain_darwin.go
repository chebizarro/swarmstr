//go:build darwin

package secrets

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

const osSecretService = "metiq"

type osBackend struct{}

func NewOSBackend() SecretBackend { return osBackend{} }

func (osBackend) Name() string { return "macos-keychain" }

func (osBackend) Get(key string) (string, bool, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", osSecretService, "-a", key, "-w")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && bytes.Contains(exitErr.Stderr, []byte("could not be found")) {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSuffix(string(out), "\n"), true, nil
}

func (osBackend) Set(key, value string) error {
	if err := exec.Command("security", "add-generic-password", "-U", "-s", osSecretService, "-a", key, "-w", value).Run(); err != nil {
		return fmt.Errorf("security add-generic-password: %w", err)
	}
	return nil
}

func (osBackend) Delete(key string) error {
	cmd := exec.Command("security", "delete-generic-password", "-s", osSecretService, "-a", key)
	if err := cmd.Run(); err != nil {
		return nil
	}
	return nil
}
