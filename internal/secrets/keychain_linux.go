//go:build linux

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

func (osBackend) Name() string { return "linux-secret-service" }

func (osBackend) Get(key string) (string, bool, error) {
	cmd := exec.Command("secret-tool", "lookup", "service", osSecretService, "account", key)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) == 0 {
			return "", false, nil
		}
		return "", false, err
	}
	return strings.TrimSuffix(string(out), "\n"), true, nil
}

func (osBackend) Set(key, value string) error {
	cmd := exec.Command("secret-tool", "store", "--label", fmt.Sprintf("metiq %s", key), "service", osSecretService, "account", key)
	cmd.Stdin = strings.NewReader(value)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("secret-tool store: %w", err)
	}
	return nil
}

func (osBackend) Delete(key string) error {
	_ = exec.Command("secret-tool", "clear", "service", osSecretService, "account", key).Run()
	return nil
}
