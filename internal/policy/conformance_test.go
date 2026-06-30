package policy

import (
	"testing"

	"metiq/internal/store/state"
)

func TestCheckPolicyConformanceWeakConfig(t *testing.T) {
	report := CheckPolicyConformance(state.ConfigDoc{
		Permissions: state.PermissionsConfig{DefaultBehavior: "allow"},
		Extra: map[string]any{
			"sandbox":    map[string]any{"driver": "nop"},
			"extensions": map[string]any{"demo": map[string]any{"enabled": true}},
		},
	})
	want := map[string]ConformanceStatus{
		"approval-rules":      ConformanceFail,
		"default-tool-policy": ConformanceWarn,
		"sandbox-egress":      ConformanceWarn,
		"sandbox-driver":      ConformanceFail,
		"plugin-permissions":  ConformanceWarn,
	}
	for _, f := range report.Findings {
		if want[f.ID] != f.Status {
			t.Fatalf("%s status=%s want %s", f.ID, f.Status, want[f.ID])
		}
	}
}

func TestCheckPolicyConformanceHardenedConfig(t *testing.T) {
	report := CheckPolicyConformance(state.ConfigDoc{
		Permissions: state.PermissionsConfig{
			DefaultBehavior: "ask",
			Rules:           []state.PermissionRule{{ID: "ask-exec", Behavior: "ask", Tool: "bash"}, {ID: "ask-plugin", Behavior: "ask", Origin: "plugin", Tool: "*"}},
		},
		Extra: map[string]any{
			"sandbox": map[string]any{"driver": "nsjail", "egress_enforced": true},
		},
	})
	for _, f := range report.Findings {
		if f.Status != ConformancePass {
			t.Fatalf("%s status=%s want pass (%s)", f.ID, f.Status, f.Message)
		}
	}
}
