package manifest

import (
	"strings"
	"testing"
)

func TestCheckVersionRange(t *testing.T) {
	tests := []struct {
		version, constraint string
		want                bool
	}{
		{"1.4.2", "^1.0.0", true},
		{"2.0.0", "^1.0.0", false},
		{"0.2.9", "^0.2.3", true},
		{"0.3.0", "^0.2.3", false},
		{"1.2.9", "~1.2.3", true},
		{"1.3.0", "~1.2.3", false},
		{"1.5.0", ">=1.2.0 <2.0.0", true},
		{"2.1.0", "1.x || >=3.0.0", false},
		{"3.0.0", "1.x || >=3.0.0", true},
		{"2026.3.24-beta.3", ">=2026.3.24-beta.2", true},
		{"2026.3.24-beta.1", ">=2026.3.24-beta.2", false},
	}
	for _, tt := range tests {
		t.Run(tt.version+"_"+tt.constraint, func(t *testing.T) {
			got, err := CheckVersionRange(tt.version, tt.constraint)
			if err != nil {
				t.Fatalf("CheckVersionRange failed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("CheckVersionRange(%q, %q)=%v want %v", tt.version, tt.constraint, got, tt.want)
			}
		})
	}
	if _, err := CheckVersionRange("1.0.0", "not-semver"); err == nil {
		t.Fatal("expected invalid constraint error")
	}
}

func TestManifestCompatibilityContract(t *testing.T) {
	m := &Manifest{
		SchemaVersion: SchemaVersion,
		ID:            "contract-plugin",
		Version:       "1.0.0",
		Runtime:       RuntimeGoja,
		Compat: Compatibility{
			PluginAPI:      "^1.0.0",
			MinHostVersion: ">=2.0.0",
		},
		Build: BuildInfo{HostVersion: "2.1.0", PluginSDKVersion: HostPluginSDKVersion},
	}
	if err := Validate(m); err != nil {
		t.Fatalf("valid v3 manifest rejected: %v", err)
	}
	if err := m.CheckCompatibility(HostContract{HostVersion: "2.2.0", PluginAPIVersion: HostPluginAPIVersion, PluginSDKVersion: HostPluginSDKVersion}); err != nil {
		t.Fatalf("compatible host rejected: %v", err)
	}
	if err := m.CheckCompatibility(HostContract{HostVersion: "2.2.0", PluginAPIVersion: "2.0.0"}); err == nil || !strings.Contains(err.Error(), "does not include") {
		t.Fatalf("expected API incompatibility, got %v", err)
	}
	if err := m.CheckCompatibility(HostContract{HostVersion: "1.9.0", PluginAPIVersion: HostPluginAPIVersion}); err == nil || !strings.Contains(err.Error(), "requires host") {
		t.Fatalf("expected host incompatibility, got %v", err)
	}
}

func TestManifestV3ValidatesContractAndRejectsFutureSchema(t *testing.T) {
	base := Manifest{SchemaVersion: SchemaVersion, ID: "contract-plugin", Version: "1.0.0", Runtime: RuntimeGoja}
	if err := Validate(&base); err != nil {
		t.Fatalf("legacy local manifest rejected: %v", err)
	}
	base.Compat.PluginAPI = "not-semver"
	if err := Validate(&base); err == nil || !strings.Contains(err.Error(), "compat.plugin_api") {
		t.Fatalf("expected invalid contract error, got %v", err)
	}
	base.Compat.PluginAPI = ""
	base.SchemaVersion = SchemaVersion + 1
	if err := Validate(&base); err == nil || !strings.Contains(err.Error(), "newer than host schema") {
		t.Fatalf("expected future schema rejection, got %v", err)
	}
}
