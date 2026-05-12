//go:build !darwin && !linux && !windows

package secrets

import "fmt"

type osBackend struct{}

func NewOSBackend() SecretBackend { return osBackend{} }

func (osBackend) Name() string { return "unsupported-os-secret-store" }
func (osBackend) Get(key string) (string, bool, error) {
	return "", false, fmt.Errorf("OS secret store unsupported")
}
func (osBackend) Set(key, value string) error { return fmt.Errorf("OS secret store unsupported") }
func (osBackend) Delete(key string) error     { return nil }
