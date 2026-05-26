package contracts_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"metiq/internal/plugins/hooks"
	"metiq/internal/plugins/lifecycle"
	"metiq/internal/plugins/manifest"
	plugreg "metiq/internal/plugins/registry"
	"metiq/internal/plugins/sdk"
)

func contractManifest(id string) manifest.Manifest {
	return manifest.Manifest{
		SchemaVersion: manifest.SchemaVersion,
		ID:            id,
		Version:       "1.0.0",
		Runtime:       manifest.RuntimeGoja,
		Capabilities: manifest.Capabilities{Tools: []manifest.ToolCapability{{
			Name:        "contract_tool",
			Description: "contract tool",
		}}},
	}
}

func TestManifestDiagnosticsContract_FieldPathsAllowedValuesAndHints(t *testing.T) {
	err := manifest.Validate(&manifest.Manifest{SchemaVersion: 0, ID: "Bad ID", Version: "nope", Runtime: manifest.RuntimeType("deno")})
	if err == nil {
		t.Fatal("expected validation errors")
	}
	errs, ok := err.(manifest.ValidationErrors)
	if !ok {
		t.Fatalf("expected ValidationErrors, got %T", err)
	}
	var sawRuntime bool
	for _, e := range errs {
		if e.Field == "runtime" {
			sawRuntime = true
			if len(e.AllowedValues) == 0 || !strings.Contains(e.Error(), "allowed:") || e.Hint == "" {
				t.Fatalf("runtime diagnostic missing allowed values/hint: %#v", e)
			}
		}
	}
	if !sawRuntime {
		t.Fatalf("runtime diagnostic not found in %#v", errs)
	}
}

func TestPluginContractBreadth(t *testing.T) {
	ctx := context.Background()
	t.Run("manifest valid minimal goja tool", func(t *testing.T) {
		if err := manifest.Validate(ptrManifest(contractManifest("valid-plugin"))); err != nil {
			t.Fatalf("valid manifest rejected: %v", err)
		}
	})
	t.Run("manifest rejects unsupported schema version", func(t *testing.T) {
		mf := contractManifest("schema-plugin")
		mf.SchemaVersion = 0
		expectErr(t, manifest.Validate(&mf), "schema_version")
	})
	t.Run("manifest rejects missing id", func(t *testing.T) {
		mf := contractManifest("")
		expectErr(t, manifest.Validate(&mf), "id")
	})
	t.Run("manifest rejects invalid id characters", func(t *testing.T) {
		mf := contractManifest("Bad ID")
		expectErr(t, manifest.Validate(&mf), "lowercase")
	})
	t.Run("manifest rejects invalid semver", func(t *testing.T) {
		mf := contractManifest("bad-version")
		mf.Version = "one.two.three"
		expectErr(t, manifest.Validate(&mf), "semantic")
	})
	t.Run("manifest rejects unknown runtime", func(t *testing.T) {
		mf := contractManifest("bad-runtime")
		mf.Runtime = manifest.RuntimeType("deno")
		expectErr(t, manifest.Validate(&mf), "runtime")
	})
	t.Run("manifest rejects duplicate tools", func(t *testing.T) {
		mf := contractManifest("dupe-tools")
		mf.Capabilities.Tools = append(mf.Capabilities.Tools, mf.Capabilities.Tools[0])
		expectErr(t, manifest.Validate(&mf), "duplicated")
	})
	t.Run("manifest rejects missing tool name", func(t *testing.T) {
		mf := contractManifest("missing-tool")
		mf.Capabilities.Tools[0].Name = ""
		expectErr(t, manifest.Validate(&mf), "capabilities.tools[0].name")
	})
	t.Run("manifest rejects duplicate channels", func(t *testing.T) {
		mf := contractManifest("dupe-channel")
		mf.Capabilities.Channels = []manifest.ChannelCapability{{ID: "chat"}, {ID: "chat"}}
		expectErr(t, manifest.Validate(&mf), "duplicated")
	})
	t.Run("manifest rejects mcp server without transport", func(t *testing.T) {
		mf := contractManifest("bad-mcp")
		mf.Capabilities.MCPServers = []manifest.MCPServerCapability{{ID: "server"}}
		expectErr(t, manifest.Validate(&mf), "transport")
	})
	t.Run("manifest rejects missing skill id", func(t *testing.T) {
		mf := contractManifest("bad-skill")
		mf.Capabilities.Skills = []manifest.SkillCapability{{Name: "Skill"}}
		expectErr(t, manifest.Validate(&mf), "capabilities.skills[0].id")
	})
	t.Run("manifest rejects duplicate gateway methods", func(t *testing.T) {
		mf := contractManifest("dupe-method")
		mf.Capabilities.GatewayMethods = []manifest.GatewayMethodCapability{{Method: "x.y"}, {Method: "x.y"}}
		expectErr(t, manifest.Validate(&mf), "duplicated")
	})
	t.Run("manifest rejects provider missing id", func(t *testing.T) {
		mf := contractManifest("bad-provider-id")
		mf.Capabilities.Providers = []manifest.ProviderCapability{{Type: manifest.ProviderTypeLLM}}
		expectErr(t, manifest.Validate(&mf), "capabilities.providers[0].id")
	})
	t.Run("manifest rejects provider missing type", func(t *testing.T) {
		mf := contractManifest("bad-provider-type")
		mf.Capabilities.Providers = []manifest.ProviderCapability{{ID: "provider"}}
		expectErr(t, manifest.Validate(&mf), "capabilities.providers[0].type")
	})
	t.Run("manifest compatibility honors min version", func(t *testing.T) {
		mf := contractManifest("compat-plugin")
		mf.MinMetiqVersion = "2.0.0"
		if mf.IsCompatible("1.9.9") || !mf.IsCompatible("2.0.0") {
			t.Fatalf("unexpected compatibility result")
		}
	})
	t.Run("sdk manifest rejects duplicate tools", func(t *testing.T) {
		err := sdk.ValidateManifest(sdk.Manifest{ID: "sdk-plugin", Tools: []sdk.ToolSchema{{Name: "a"}, {Name: "a"}}})
		expectErr(t, err, "duplicated")
	})
	t.Run("sdk tool schema rejects malformed required", func(t *testing.T) {
		err := sdk.ValidateToolSchema(sdk.ToolSchema{Name: "tool", Parameters: map[string]any{"type": "object", "required": []any{"ok", 42}}})
		expectErr(t, err, "array of strings")
	})
	t.Run("sdk permissions array allows wildcard namespaces", func(t *testing.T) {
		var p sdk.Permissions
		if err := json.Unmarshal([]byte(`["*"]`), &p); err != nil {
			t.Fatalf("unmarshal permissions: %v", err)
		}
		for _, ns := range []string{"log", "config", "http", "storage", "nostr", "agent", "session", "task", "memory", "webSearch"} {
			if !p.Allows(ns) {
				t.Fatalf("wildcard should allow %s", ns)
			}
		}
	})
	t.Run("sdk permissions default only allow log", func(t *testing.T) {
		var p sdk.Permissions
		if !p.Allows("log") || p.Allows("storage") || p.Allows("agent") {
			t.Fatalf("default permissions should allow log only")
		}
	})
	t.Run("unified registry close rejects late registration", func(t *testing.T) {
		reg := plugreg.NewUnifiedRegistry()
		reg.CloseRegistrationWindow()
		err := reg.RegisterFromGojaManifest(sdk.Manifest{ID: "late", Version: "1.0.0", Tools: []sdk.ToolSchema{{Name: "tool"}}})
		expectErr(t, err, "closed")
	})
	t.Run("unified registry unregister remains allowed after close", func(t *testing.T) {
		reg := plugreg.NewUnifiedRegistry()
		if err := reg.RegisterFromGojaManifest(sdk.Manifest{ID: "loaded", Version: "1.0.0", Tools: []sdk.ToolSchema{{Name: "tool"}}}); err != nil {
			t.Fatalf("register: %v", err)
		}
		reg.CloseRegistrationWindow()
		if err := reg.UnregisterPlugin("loaded"); err != nil {
			t.Fatalf("unregister after close: %v", err)
		}
	})
	t.Run("lifecycle rejects invalid install scope", func(t *testing.T) {
		mgr := lifecycle.NewManager(lifecycle.LifecycleConfig{AutoEnable: false}, t.TempDir())
		_, err := mgr.Install(ctx, contractManifest("scope-plugin"), t.TempDir(), lifecycle.InstallOptions{Scope: lifecycle.Scope("global")})
		expectErr(t, err, "invalid scope")
	})
	t.Run("lifecycle force reinstall replaces same scope", func(t *testing.T) {
		mgr := lifecycle.NewManager(lifecycle.LifecycleConfig{AutoEnable: false}, t.TempDir())
		mf := contractManifest("force-plugin")
		if _, err := mgr.Install(ctx, mf, t.TempDir(), lifecycle.InstallOptions{Scope: lifecycle.ScopeLocal}); err != nil {
			t.Fatalf("install: %v", err)
		}
		mf.Version = "1.0.1"
		if _, err := mgr.Install(ctx, mf, t.TempDir(), lifecycle.InstallOptions{Scope: lifecycle.ScopeLocal, Force: true}); err != nil {
			t.Fatalf("force install: %v", err)
		}
		got, ok := mgr.ResolveByScope(mf.ID, lifecycle.ScopeLocal)
		if !ok || got.Manifest.Version != "1.0.1" {
			t.Fatalf("force reinstall did not replace plugin: %#v", got)
		}
	})
	t.Run("lifecycle skill export requires config opt in", func(t *testing.T) {
		mgr := lifecycle.NewManager(lifecycle.LifecycleConfig{AutoEnable: false, AllowSkillExport: false}, t.TempDir())
		mf := contractManifest("skill-export")
		mf.Capabilities.Skills = []manifest.SkillCapability{{ID: "s", Exportable: true}}
		_, err := mgr.Install(ctx, mf, t.TempDir(), lifecycle.InstallOptions{Scope: lifecycle.ScopeLocal, ExportSkills: true})
		expectErr(t, err, "skill export is disabled")
	})
	t.Run("hook invoker stop on mutation", func(t *testing.T) {
		inv := hooks.NewHookInvoker(nil, nil)
		inv.RegisterNative(plugreg.HookBeforeToolCall, "first", 10, func(context.Context, any) (any, error) {
			return map[string]any{"mutation": map[string]any{"tool": "patched"}}, nil
		})
		inv.RegisterNative(plugreg.HookBeforeToolCall, "second", 20, func(context.Context, any) (any, error) {
			return map[string]any{"mutation": map[string]any{"tool": "late"}}, nil
		})
		res, err := inv.Emit(ctx, plugreg.HookBeforeToolCall, map[string]any{}, hooks.EmitOptions{StopOnMutation: true})
		if err != nil || len(res.Results) != 1 || res.Mutation["tool"] != "patched" {
			t.Fatalf("unexpected mutation result res=%#v err=%v", res, err)
		}
	})
	t.Run("hook invoker stop on rejection", func(t *testing.T) {
		inv := hooks.NewHookInvoker(nil, nil)
		inv.RegisterNative(plugreg.HookBeforeDispatch, "reject", 10, func(context.Context, any) (any, error) {
			return map[string]any{"reject": true, "reason": "blocked"}, nil
		})
		inv.RegisterNative(plugreg.HookBeforeDispatch, "late", 20, func(context.Context, any) (any, error) { return map[string]any{"ok": true}, nil })
		res, err := inv.Emit(ctx, plugreg.HookBeforeDispatch, map[string]any{}, hooks.EmitOptions{StopOnReject: true})
		if err != nil || !res.Rejected || res.RejectReason != "blocked" || len(res.Results) != 1 {
			t.Fatalf("unexpected rejection result res=%#v err=%v", res, err)
		}
	})
	t.Run("hook invoker reports native panic", func(t *testing.T) {
		inv := hooks.NewHookInvoker(nil, nil)
		inv.RegisterNative(plugreg.HookAgentEnd, "panic", 10, func(context.Context, any) (any, error) { panic("boom") })
		res, err := inv.Emit(ctx, plugreg.HookAgentEnd, map[string]any{}, hooks.EmitOptions{StopOnError: true, HandlerTimeout: time.Second})
		if err == nil || res.Error == nil || !strings.Contains(err.Error(), "panic") {
			t.Fatalf("expected panic error, res=%#v err=%v", res, err)
		}
	})
}

func TestLifecycleContract_ScopedInstallEnableDisableAndRegistryRefresh(t *testing.T) {
	ctx := context.Background()
	mgr := lifecycle.NewManager(lifecycle.LifecycleConfig{AutoEnable: false}, t.TempDir())
	mf := contractManifest("contract-plugin")
	installed, err := mgr.Install(ctx, mf, t.TempDir(), lifecycle.InstallOptions{Scope: lifecycle.ScopeLocal})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if installed.State != lifecycle.StateInstalled {
		t.Fatalf("initial state = %s", installed.State)
	}
	if err := mgr.EnableByScope(ctx, mf.ID, lifecycle.ScopeLocal); err != nil {
		t.Fatalf("EnableByScope: %v", err)
	}
	if err := mgr.RefreshRegistry(); err != nil {
		t.Fatalf("RefreshRegistry: %v", err)
	}
	enabled := mgr.ListEnabled()
	if len(enabled) != 1 || enabled[0].PluginID != mf.ID {
		t.Fatalf("enabled plugins = %#v", enabled)
	}
	if err := mgr.DisableByScope(ctx, mf.ID, lifecycle.ScopeLocal); err != nil {
		t.Fatalf("DisableByScope: %v", err)
	}
	if len(mgr.ListEnabled()) != 0 {
		t.Fatalf("expected no enabled plugins after disable")
	}
}

func ptrManifest(m manifest.Manifest) *manifest.Manifest { return &m }

func expectErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q", want)
	}
	if !strings.Contains(strings.ToLower(fmt.Sprint(err)), strings.ToLower(want)) {
		t.Fatalf("error %q does not contain %q", err, want)
	}
}
