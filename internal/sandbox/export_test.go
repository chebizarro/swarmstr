package sandbox

import (
	"context"
	"testing"
)

// StubDockerAvailability replaces the Docker daemon availability probe for the
// duration of a test. Construction tests exercise driver resolution and config
// mapping, not container execution, so they must stay hermetic instead of
// depending on a warm Docker daemon on the host or CI runner.
func StubDockerAvailability(t *testing.T, err error) {
	t.Helper()
	orig := dockerAvailabilityCheck
	dockerAvailabilityCheck = func(context.Context) error { return err }
	t.Cleanup(func() { dockerAvailabilityCheck = orig })
}
