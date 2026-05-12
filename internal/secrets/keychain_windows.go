//go:build windows

package secrets

import (
	"fmt"
	"os/exec"
	"strings"
)

const osSecretService = "metiq"

type osBackend struct{}

func NewOSBackend() SecretBackend { return osBackend{} }

func (osBackend) Name() string { return "windows-credential-manager" }

func (osBackend) Get(key string) (string, bool, error) {
	return "", false, fmt.Errorf("windows credential manager read is not available without native API support")
}

func (osBackend) Set(key, value string) error {
	target := osSecretService + ":" + key
	if err := exec.Command("cmdkey", "/generic:"+target, "/user:"+key, "/pass:"+value).Run(); err != nil {
		return fmt.Errorf("cmdkey store: %w", err)
	}
	return nil
}

func (osBackend) Delete(key string) error {
	target := osSecretService + ":" + strings.TrimSpace(key)
	_ = exec.Command("cmdkey", "/delete:"+target).Run()
	return nil
}
