package installer

import "testing"

func TestExtractNPMPackageName(t *testing.T) {
	tests := []struct {
		spec string
		want string
	}{
		{spec: "my-package", want: "my-package"},
		{spec: "my-package@1.2.3", want: "my-package"},
		{spec: "@scope/package", want: "@scope/package"},
		{spec: "@scope/package@1.2.3", want: "@scope/package"},
		{spec: " @scope/package@latest ", want: "@scope/package"},
	}

	for _, tc := range tests {
		got := extractNPMPackageName(tc.spec)
		if got != tc.want {
			t.Fatalf("extractNPMPackageName(%q) = %q, want %q", tc.spec, got, tc.want)
		}
	}
}
