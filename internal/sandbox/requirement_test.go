package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type requirementTestRunner struct{ driver string }

func (r requirementTestRunner) Driver() string { return r.driver }
func (r requirementTestRunner) Run(context.Context, []string, []string, string) (Result, error) {
	return Result{Driver: r.driver}, nil
}

func TestRequiredSessionRequirementPersistsAndOverridesExecutionDriver(t *testing.T) {
	const backendName = "required-test"
	if err := RegisterBackend(BackendFunc{BackendName: backendName, Constructor: func(Config) (SandboxRunner, error) {
		return requirementTestRunner{driver: backendName}, nil
	}}); err != nil {
		t.Fatal(err)
	}

	requirement, err := NewSessionRequirement("support", CreatorSandboxRequired, backendName)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(requirement)
	if err != nil {
		t.Fatal(err)
	}
	var persisted SessionRequirement
	if err := json.Unmarshal(encoded, &persisted); err != nil {
		t.Fatal(err)
	}

	runner, err := NewForSession(Config{Driver: "nop", AllowUnsafeNop: true}, persisted)
	if err != nil {
		t.Fatal(err)
	}
	if runner.Driver() != backendName {
		t.Fatalf("execution-time driver override bypassed creator requirement: %q", runner.Driver())
	}
}

func TestRequiredSessionRequirementFailsClosedWhenBackendUnavailable(t *testing.T) {
	const backendName = "required-fail-test"
	if err := RegisterBackend(BackendFunc{BackendName: backendName, Constructor: func(Config) (SandboxRunner, error) {
		return nil, errors.New("backend offline")
	}}); err != nil {
		t.Fatal(err)
	}
	requirement, err := NewSessionRequirement("automation", CreatorSandboxRequired, backendName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewForSession(Config{}, requirement); err == nil || !strings.Contains(err.Error(), "required sandbox backend") {
		t.Fatalf("unavailable required backend did not fail closed: %v", err)
	}
}

func TestRequiredSessionRequirementRejectsUnsafeOrTamperedState(t *testing.T) {
	if _, err := NewSessionRequirement("support", CreatorSandboxRequired, "nop"); err == nil {
		t.Fatal("required creator policy accepted host execution backend")
	}
	tampered := SessionRequirement{
		Version: sessionRequirementVersion, CreatorRole: "support",
		Policy: CreatorSandboxRequired, Backend: "nop",
	}
	StubDockerAvailability(t, nil)
	if _, err := NewForSession(Config{Driver: "docker"}, tampered); err == nil {
		t.Fatal("tampered persisted requirement was accepted")
	}
}
