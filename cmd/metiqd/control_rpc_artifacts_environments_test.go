package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	artifactspkg "metiq/internal/gateway/artifacts"
	environmentspkg "metiq/internal/gateway/environments"
	"metiq/internal/gateway/methods"
	nostruntime "metiq/internal/nostr/runtime"
	"metiq/internal/sandbox"
	"metiq/internal/store/state"
)

func artifactsEnvironmentsCall(t *testing.T, h controlRPCHandler, cfg state.ConfigDoc, method, params string) (nostruntime.ControlRPCResult, error) {
	t.Helper()
	result, handled, err := h.handleWorkspaceSurfaceRPC(context.Background(), nostruntime.ControlRPCInbound{
		Method: method,
		Params: json.RawMessage(params),
	}, method, cfg)
	if !handled {
		t.Fatalf("method %s was not handled by workspace surface dispatch", method)
	}
	return result, err
}

func TestArtifactsRPCLifecycle(t *testing.T) {
	workspaceDir := t.TempDir()
	t.Setenv("METIQ_WORKSPACE", workspaceDir)
	h := newControlRPCHandler(controlRPCDeps{})
	cfg := state.ConfigDoc{}

	// Empty store lists cleanly.
	result, err := artifactsEnvironmentsCall(t, h, cfg, methods.MethodArtifactsList, `{}`)
	if err != nil {
		t.Fatalf("artifacts.list: %v", err)
	}
	if list := result.Result.(map[string]any)["artifacts"].([]artifactspkg.Summary); len(list) != 0 {
		t.Fatalf("expected empty artifact list, got %+v", list)
	}

	// Seed the workspace-rooted store the handlers derive from config.
	store := artifactspkg.NewStore(filepath.Join(workspaceDir, ".metiq", "artifacts"))
	seeded, err := store.Put(artifactspkg.PutRequest{
		Type:       "file",
		Title:      "report.txt",
		MimeType:   "text/plain",
		SessionKey: "sess-1",
		RunID:      "run-1",
		Data:       []byte("artifact payload"),
	})
	if err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	result, err = artifactsEnvironmentsCall(t, h, cfg, methods.MethodArtifactsList, `{"sessionKey":"sess-1","runId":"run-1"}`)
	if err != nil {
		t.Fatalf("artifacts.list scoped: %v", err)
	}
	list := result.Result.(map[string]any)["artifacts"].([]artifactspkg.Summary)
	if len(list) != 1 || list[0].ID != seeded.ID {
		t.Fatalf("scoped list mismatch: %+v", list)
	}

	result, err = artifactsEnvironmentsCall(t, h, cfg, methods.MethodArtifactsGet, `{"artifactId":"`+seeded.ID+`"}`)
	if err != nil {
		t.Fatalf("artifacts.get: %v", err)
	}
	if got := result.Result.(map[string]any)["artifact"].(artifactspkg.Summary); got.Title != "report.txt" {
		t.Fatalf("get mismatch: %+v", got)
	}

	// Scope filters gate visibility on lookups.
	if _, err := artifactsEnvironmentsCall(t, h, cfg, methods.MethodArtifactsGet, `{"artifactId":"`+seeded.ID+`","sessionKey":"other"}`); err == nil {
		t.Fatal("expected scope miss for artifacts.get")
	}

	result, err = artifactsEnvironmentsCall(t, h, cfg, methods.MethodArtifactsDownload, `{"artifactId":"`+seeded.ID+`"}`)
	if err != nil {
		t.Fatalf("artifacts.download: %v", err)
	}
	payload := result.Result.(map[string]any)
	if payload["encoding"] != "base64" {
		t.Fatalf("unexpected download payload: %+v", payload)
	}
	data, err := base64.StdEncoding.DecodeString(payload["data"].(string))
	if err != nil || string(data) != "artifact payload" {
		t.Fatalf("download decode: %v %q", err, data)
	}

	if _, err := artifactsEnvironmentsCall(t, h, cfg, methods.MethodArtifactsGet, `{}`); err == nil || !strings.Contains(err.Error(), "artifactId is required") {
		t.Fatalf("expected artifactId validation error, got %v", err)
	}
}

func newEnvironmentsTestHandler() controlRPCHandler {
	mgr := environmentspkg.NewManager(environmentspkg.Options{
		CheckDocker: func(context.Context) error { return nil },
		NewRunner: func(cfg sandbox.Config) (sandbox.SandboxRunner, error) {
			return envTestRunner{}, nil
		},
	})
	return newControlRPCHandler(controlRPCDeps{environments: mgr})
}

type envTestRunner struct{}

func (envTestRunner) Driver() string { return "docker" }
func (envTestRunner) Run(context.Context, []string, []string, string) (sandbox.Result, error) {
	return sandbox.Result{}, nil
}

func environmentsTestConfig() state.ConfigDoc {
	return state.ConfigDoc{Extra: map[string]any{
		"environments": map[string]any{
			"profiles": map[string]any{
				"default": map[string]any{"driver": "docker", "docker_image": "alpine:3"},
			},
		},
	}}
}

func TestEnvironmentsRPCLifecycle(t *testing.T) {
	h := newEnvironmentsTestHandler()
	cfg := environmentsTestConfig()

	result, err := artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsList, `{}`)
	if err != nil {
		t.Fatalf("environments.list: %v", err)
	}
	listResult := result.Result.(map[string]any)
	envs := listResult["environments"].([]environmentspkg.Summary)
	if len(envs) != 1 || envs[0].ID != "gateway" || envs[0].Status != "available" {
		t.Fatalf("expected gateway-only inventory, got %+v", envs)
	}
	profiles := listResult["profiles"].([]environmentspkg.Profile)
	if len(profiles) != 1 || profiles[0].ID != "default" || profiles[0].ProviderID != "sandbox:docker" {
		t.Fatalf("unexpected profiles: %+v", profiles)
	}

	result, err = artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsCreate, `{"profileId":"default","idempotencyKey":"idem-1"}`)
	if err != nil {
		t.Fatalf("environments.create: %v", err)
	}
	created := result.Result.(environmentspkg.Summary)
	if created.Status != "available" || created.Worker == nil || created.Worker.State != environmentspkg.StateReady {
		t.Fatalf("unexpected create summary: %+v", created)
	}

	// Idempotent replay returns the same environment.
	result, err = artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsCreate, `{"profileId":"default","idempotencyKey":"idem-1"}`)
	if err != nil {
		t.Fatalf("environments.create replay: %v", err)
	}
	if replay := result.Result.(environmentspkg.Summary); replay.ID != created.ID {
		t.Fatalf("idempotency violated: %+v vs %+v", replay, created)
	}

	result, err = artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsStatus, `{"environmentId":"`+created.ID+`"}`)
	if err != nil {
		t.Fatalf("environments.status: %v", err)
	}
	if status := result.Result.(environmentspkg.Summary); status.ID != created.ID || status.Status != "available" {
		t.Fatalf("unexpected status: %+v", status)
	}

	// The static gateway environment is addressable by id.
	result, err = artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsStatus, `{"environmentId":"gateway"}`)
	if err != nil {
		t.Fatalf("environments.status gateway: %v", err)
	}
	if status := result.Result.(environmentspkg.Summary); status.Type != "local" {
		t.Fatalf("unexpected gateway status: %+v", status)
	}

	result, err = artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsDestroy, `{"environmentId":"`+created.ID+`"}`)
	if err != nil {
		t.Fatalf("environments.destroy: %v", err)
	}
	if destroyed := result.Result.(environmentspkg.Summary); destroyed.Status != "unavailable" {
		t.Fatalf("unexpected destroy summary: %+v", destroyed)
	}

	if _, err := artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsCreate, `{"profileId":"missing","idempotencyKey":"idem-2"}`); err == nil || !strings.Contains(err.Error(), "unknown environment profile") {
		t.Fatalf("expected unknown profile error, got %v", err)
	}
	if _, err := artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsStatus, `{"environmentId":"env-999"}`); err == nil {
		t.Fatal("expected unknown environment error")
	}
	if _, err := artifactsEnvironmentsCall(t, h, cfg, methods.MethodEnvironmentsCreate, `{"profileId":"default"}`); err == nil || !strings.Contains(err.Error(), "idempotencyKey is required") {
		t.Fatalf("expected idempotencyKey validation error, got %v", err)
	}
}

func TestEnvironmentsRPCFailsClosedWithoutDocker(t *testing.T) {
	mgr := environmentspkg.NewManager(environmentspkg.Options{
		CheckDocker: func(context.Context) error { return context.DeadlineExceeded },
	})
	h := newControlRPCHandler(controlRPCDeps{environments: mgr})
	_, err := artifactsEnvironmentsCall(t, h, environmentsTestConfig(), methods.MethodEnvironmentsCreate, `{"profileId":"default","idempotencyKey":"idem-x"}`)
	if err == nil || !strings.Contains(err.Error(), "docker required but unavailable") {
		t.Fatalf("expected fail-closed docker error, got %v", err)
	}
}

func TestEnvironmentsRPCUnavailableWithoutManager(t *testing.T) {
	h := newControlRPCHandler(controlRPCDeps{})
	if _, err := artifactsEnvironmentsCall(t, h, state.ConfigDoc{}, methods.MethodEnvironmentsCreate, `{"profileId":"p","idempotencyKey":"k"}`); err == nil || !strings.Contains(err.Error(), "environments are not available") {
		t.Fatalf("expected unavailability error, got %v", err)
	}
	// list still succeeds with the static gateway environment.
	result, err := artifactsEnvironmentsCall(t, h, state.ConfigDoc{}, methods.MethodEnvironmentsList, `{}`)
	if err != nil {
		t.Fatalf("environments.list without manager: %v", err)
	}
	envs := result.Result.(map[string]any)["environments"].([]environmentspkg.Summary)
	if len(envs) != 1 || envs[0].ID != "gateway" {
		t.Fatalf("unexpected inventory: %+v", envs)
	}
}
