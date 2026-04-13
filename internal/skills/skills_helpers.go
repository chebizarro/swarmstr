package skills

import (
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
)

// CheckRequirements verifies all requirement fields and returns which are missing.
// eligible is true when nothing is missing.
func CheckRequirements(req Requirements) (missing Requirements, eligible bool) {
	// Bins: all must be present.
	for _, bin := range req.Bins {
		if !BinExists(bin) {
			missing.Bins = append(missing.Bins, bin)
		}
	}

	// AnyBins: at least one must be present.
	if len(req.AnyBins) > 0 {
		found := false
		for _, bin := range req.AnyBins {
			if BinExists(bin) {
				found = true
				break
			}
		}
		if !found {
			missing.AnyBins = append(missing.AnyBins, req.AnyBins...)
		}
	}

	// Env: all must be set.
	for _, envVar := range req.Env {
		if os.Getenv(envVar) == "" {
			missing.Env = append(missing.Env, envVar)
		}
	}

	// OS: current OS must be in list (if list is non-empty).
	if len(req.OS) > 0 {
		currentOS := runtime.GOOS
		found := false
		for _, osName := range req.OS {
			if strings.EqualFold(strings.TrimSpace(osName), currentOS) {
				found = true
				break
			}
		}
		if !found {
			missing.OS = req.OS
		}
	}

	eligible = len(missing.Bins) == 0 &&
		len(missing.AnyBins) == 0 &&
		len(missing.Env) == 0 &&
		len(missing.OS) == 0 &&
		len(missing.Config) == 0
	return missing, eligible
}

// BinExists returns true when name can be found on the system PATH.
// BinExists returns true when name can be found on the system PATH.
func BinExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ─── Workspace config helper ──────────────────────────────────────────────────

// WorkspaceDir resolves the agent workspace directory from config.
// Resolution order:
//  1. extra["skills"]["workspace"] config key
//  2. METIQ_WORKSPACE env var
//  3. ~/metiq/workspace/<agentID>
//
// AggregateBins collects all unique bin names declared across a set of skills.
func AggregateBins(skills []*Skill) []string {
	seen := map[string]struct{}{}
	var out []string
	push := func(bin string) {
		bin = strings.TrimSpace(bin)
		if bin == "" {
			return
		}
		if _, ok := seen[bin]; ok {
			return
		}
		seen[bin] = struct{}{}
		out = append(out, bin)
	}
	for _, s := range skills {
		// Legacy YAML bins.
		for _, b := range s.Manifest.Bins {
			push(b)
		}
		req := s.EffectiveRequirements()
		for _, b := range req.Bins {
			push(b)
		}
		for _, b := range req.AnyBins {
			push(b)
		}
		// Install spec bins (all options, not just active one).
		for _, spec := range s.InstallSpecs() {
			for _, b := range spec.Bins {
				push(b)
			}
		}
	}
	sort.Strings(out)
	return out
}

func normalizeAndValidateInstallSpec(spec InstallSpec) (InstallSpec, bool) {
	spec.ID = strings.TrimSpace(spec.ID)
	spec.Kind = normalizeInstallKind(spec.Kind)
	spec.Formula = strings.TrimSpace(spec.Formula)
	spec.Package = strings.TrimSpace(spec.Package)
	spec.Module = strings.TrimSpace(spec.Module)
	spec.URL = strings.TrimSpace(spec.URL)
	spec.Label = strings.TrimSpace(spec.Label)
	spec.Bins = compactStringSlice(spec.Bins)
	spec.OS = compactStringSlice(spec.OS)

	switch spec.Kind {
	case "brew":
		if spec.Formula == "" && spec.Package == "" {
			return InstallSpec{}, false
		}
		if !isSafePackageToken(coalesceInstallValue(spec.Formula, spec.Package)) {
			return InstallSpec{}, false
		}
	case "node", "apt", "uv":
		if spec.Package == "" || !isSafePackageToken(spec.Package) {
			return InstallSpec{}, false
		}
	case "go":
		if spec.Module == "" || strings.Contains(spec.Module, "://") || strings.HasPrefix(spec.Module, "-") {
			return InstallSpec{}, false
		}
	case "download":
		u, err := url.Parse(spec.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return InstallSpec{}, false
		}
	default:
		return InstallSpec{}, false
	}
	return spec, true
}

func normalizeInstallKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "npm", "node":
		return "node"
	case "brew", "go", "uv", "download", "apt":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return strings.ToLower(strings.TrimSpace(kind))
	}
}

func compactStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func isSafePackageToken(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || strings.HasPrefix(v, "-") {
		return false
	}
	return !strings.ContainsAny(v, " \t\r\n")
}

func coalesceInstallValue(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}
