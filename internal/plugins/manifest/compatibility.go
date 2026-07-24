package manifest

import (
	"fmt"
	"runtime/debug"
	"strconv"
	"strings"
)

// HostPluginAPIVersion is the compatibility version implemented by the plugin
// host. It is intentionally independent from the swarmstr release version.
const HostPluginAPIVersion = "1.0.0"

// HostPluginSDKVersion is the SDK contract version exposed to plugins.
const HostPluginSDKVersion = "1.0.0"

// Compatibility declares the host contracts required by a plugin.
type Compatibility struct {
	PluginAPI      string `json:"plugin_api,omitempty"`
	MinHostVersion string `json:"min_host_version,omitempty"`
}

// BuildInfo records the host and SDK versions used to build a plugin.
type BuildInfo struct {
	HostVersion      string `json:"host_version,omitempty"`
	PluginSDKVersion string `json:"plugin_sdk_version,omitempty"`
}

// HostContract describes the versions offered by a running plugin host.
type HostContract struct {
	HostVersion      string
	PluginAPIVersion string
	PluginSDKVersion string
}

// CurrentHostContract returns the contracts offered by the running binary. Go
// release builds expose their module version through build info; local builds
// use a stable semver-compatible development version.
func CurrentHostContract() HostContract {
	hostVersion := "0.0.0-dev"
	if info, ok := debug.ReadBuildInfo(); ok {
		candidate := strings.TrimSpace(info.Main.Version)
		if candidate != "" && candidate != "(devel)" {
			hostVersion = strings.TrimPrefix(candidate, "v")
		}
	}
	return HostContract{
		HostVersion:      hostVersion,
		PluginAPIVersion: HostPluginAPIVersion,
		PluginSDKVersion: HostPluginSDKVersion,
	}
}

// CheckCompatibility returns a descriptive error when a manifest cannot run
// against host. Legacy min_metiq_version remains supported.
func (m *Manifest) CheckCompatibility(host HostContract) error {
	if m == nil {
		return fmt.Errorf("plugin manifest is nil")
	}
	apiRange := strings.TrimSpace(m.Compat.PluginAPI)
	if apiRange != "" {
		if strings.TrimSpace(host.PluginAPIVersion) == "" {
			return fmt.Errorf("plugin requires API %q but host plugin API version is unknown", apiRange)
		}
		ok, err := CheckVersionRange(host.PluginAPIVersion, apiRange)
		if err != nil {
			return fmt.Errorf("invalid plugin API range %q: %w", apiRange, err)
		}
		if !ok {
			return fmt.Errorf("plugin API %q does not include host API %s", apiRange, host.PluginAPIVersion)
		}
	}
	minHost := strings.TrimSpace(m.Compat.MinHostVersion)
	if minHost == "" {
		minHost = strings.TrimSpace(m.MinMetiqVersion)
	}
	if minHost != "" {
		if strings.TrimSpace(host.HostVersion) == "" {
			return fmt.Errorf("plugin requires host version %q but host version is unknown", minHost)
		}
		constraint := minHost
		if !strings.ContainsAny(constraint, "<>=~^*xX| ") {
			constraint = ">=" + constraint
		}
		ok, err := CheckVersionRange(host.HostVersion, constraint)
		if err != nil {
			return fmt.Errorf("invalid minimum host version %q: %w", minHost, err)
		}
		if !ok {
			return fmt.Errorf("plugin requires host %q but host is %s", minHost, host.HostVersion)
		}
	}
	return nil
}

// CheckVersionRange evaluates the common npm semver range forms used by plugin
// packages: comparators, whitespace conjunctions, || alternatives, caret,
// tilde, exact versions, and x/* wildcards.
func CheckVersionRange(version, constraint string) (bool, error) {
	v, err := parseContractVersion(version)
	if err != nil {
		return false, fmt.Errorf("invalid version %q: %w", version, err)
	}
	constraint = strings.TrimSpace(constraint)
	if constraint == "" || constraint == "*" {
		return true, nil
	}
	for _, alternative := range strings.Split(constraint, "||") {
		alternative = strings.TrimSpace(alternative)
		if alternative == "" {
			continue
		}
		alternative = strings.NewReplacer(">= ", ">=", "<= ", "<=", "> ", ">", "< ", "<", "= ", "=", "^ ", "^", "~ ", "~").Replace(alternative)
		tokens := strings.Fields(strings.ReplaceAll(alternative, ",", " "))
		matched := true
		for _, token := range tokens {
			ok, err := matchContractToken(v, token)
			if err != nil {
				return false, err
			}
			if !ok {
				matched = false
				break
			}
		}
		if matched && len(tokens) > 0 {
			return true, nil
		}
	}
	return false, nil
}

type contractVersion struct {
	major, minor, patch int
	pre                 []string
}

func parseContractVersion(raw string) (contractVersion, error) {
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if raw == "" {
		return contractVersion{}, fmt.Errorf("empty version")
	}
	if plus := strings.IndexByte(raw, '+'); plus >= 0 {
		raw = raw[:plus]
	}
	var pre []string
	if dash := strings.IndexByte(raw, '-'); dash >= 0 {
		pre = strings.Split(raw[dash+1:], ".")
		raw = raw[:dash]
	}
	parts := strings.Split(raw, ".")
	if len(parts) > 3 || len(parts) == 0 {
		return contractVersion{}, fmt.Errorf("expected major[.minor[.patch]]")
	}
	num := [3]int{}
	for i, part := range parts {
		if part == "" {
			return contractVersion{}, fmt.Errorf("empty numeric component")
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return contractVersion{}, fmt.Errorf("invalid numeric component %q", part)
		}
		num[i] = n
	}
	return contractVersion{major: num[0], minor: num[1], patch: num[2], pre: pre}, nil
}

func compareContractVersions(a, b contractVersion) int {
	for _, pair := range [][2]int{{a.major, b.major}, {a.minor, b.minor}, {a.patch, b.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(a.pre) == 0 && len(b.pre) == 0 {
		return 0
	}
	if len(a.pre) == 0 {
		return 1
	}
	if len(b.pre) == 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		if a.pre[i] == b.pre[i] {
			continue
		}
		an, aerr := strconv.Atoi(a.pre[i])
		bn, berr := strconv.Atoi(b.pre[i])
		switch {
		case aerr == nil && berr == nil:
			if an < bn {
				return -1
			}
			return 1
		case aerr == nil:
			return -1
		case berr == nil:
			return 1
		case a.pre[i] < b.pre[i]:
			return -1
		default:
			return 1
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}

func matchContractToken(version contractVersion, token string) (bool, error) {
	token = strings.TrimSpace(token)
	if token == "" || token == "*" {
		return true, nil
	}
	op := ""
	for _, candidate := range []string{">=", "<=", ">", "<", "=", "^", "~"} {
		if strings.HasPrefix(token, candidate) {
			op = candidate
			token = strings.TrimSpace(strings.TrimPrefix(token, candidate))
			break
		}
	}
	if hasWildcard(token) {
		if op != "" && op != "=" {
			return false, fmt.Errorf("wildcard cannot be used with %q", op)
		}
		return matchWildcard(version, token)
	}
	target, err := parseContractVersion(token)
	if err != nil {
		return false, fmt.Errorf("invalid range token %q: %w", token, err)
	}
	cmp := compareContractVersions(version, target)
	switch op {
	case ">=":
		return cmp >= 0, nil
	case "<=":
		return cmp <= 0, nil
	case ">":
		return cmp > 0, nil
	case "<":
		return cmp < 0, nil
	case "^":
		upper := target
		switch {
		case target.major > 0:
			upper = contractVersion{major: target.major + 1}
		case target.minor > 0:
			upper = contractVersion{minor: target.minor + 1}
		default:
			upper = contractVersion{patch: target.patch + 1}
		}
		return cmp >= 0 && compareContractVersions(version, upper) < 0, nil
	case "~":
		upper := contractVersion{major: target.major, minor: target.minor + 1}
		return cmp >= 0 && compareContractVersions(version, upper) < 0, nil
	case "", "=":
		return cmp == 0, nil
	default:
		return false, fmt.Errorf("unsupported range operator %q", op)
	}
}

func hasWildcard(raw string) bool {
	for _, part := range strings.Split(raw, ".") {
		if part == "*" || strings.EqualFold(part, "x") {
			return true
		}
	}
	return false
}

func matchWildcard(version contractVersion, raw string) (bool, error) {
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(raw), "v"), ".")
	if len(parts) > 3 {
		return false, fmt.Errorf("invalid wildcard range %q", raw)
	}
	actual := []int{version.major, version.minor, version.patch}
	for i, part := range parts {
		if part == "*" || strings.EqualFold(part, "x") {
			return true, nil
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false, fmt.Errorf("invalid wildcard component %q", part)
		}
		if actual[i] != n {
			return false, nil
		}
	}
	return true, nil
}
