package main

import (
	"context"
	"testing"

	"metiq/internal/gateway/methods"
	"metiq/internal/sandbox"
	"metiq/internal/store/state"
)

type sessionRequirementRunner struct{ driver string }

func (r sessionRequirementRunner) Driver() string { return r.driver }
func (r sessionRequirementRunner) Run(context.Context, []string, []string, string) (sandbox.Result, error) {
	return sandbox.Result{Driver: r.driver}, nil
}

func TestApplySandboxRunEnforcesPersistedSessionRequirement(t *testing.T) {
	const backendName = "session-required-test"
	if err := sandbox.RegisterBackend(sandbox.BackendFunc{BackendName: backendName, Constructor: func(sandbox.Config) (sandbox.SandboxRunner, error) {
		return sessionRequirementRunner{driver: backendName}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	requirement, err := sandbox.NewSessionRequirement("operator", sandbox.CreatorSandboxRequired, backendName)
	if err != nil {
		t.Fatal(err)
	}
	docs := state.NewDocsRepository(newTestStore(), "author")
	if _, err := docs.PutSession(context.Background(), "required-session", state.SessionDoc{
		Version: 1, SessionID: "required-session", SandboxRequirement: requirement,
	}); err != nil {
		t.Fatal(err)
	}
	configState := newRuntimeConfigStore(state.ConfigDoc{Extra: map[string]any{
		"sandbox": map[string]any{"driver": backendName},
	}})
	out, err := applySandboxRun(context.Background(), configState, docs, methods.SandboxRunRequest{
		SessionID: "required-session", Cmd: []string{"ignored"}, Driver: "unknown-host-override",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["driver"] != backendName {
		t.Fatalf("persisted requirement was bypassed: %+v", out)
	}
}
