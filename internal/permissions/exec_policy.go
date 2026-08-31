package permissions

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ExecSecurity is the effective host-execution security level. Lower values in
// the ordering are more restrictive and therefore win caller/host merges.
type ExecSecurity string

const (
	ExecSecurityDeny      ExecSecurity = "deny"
	ExecSecurityAllowlist ExecSecurity = "allowlist"
	ExecSecurityFull      ExecSecurity = "full"
)

// ExecAsk controls when an otherwise-eligible execution requires a reviewer.
type ExecAsk string

const (
	ExecAskOff    ExecAsk = "off"
	ExecAskOnMiss ExecAsk = "on-miss"
	ExecAskAlways ExecAsk = "always"
)

// ExecAllowlistEntry is a normalized command policy entry. Signature is the
// legacy/canonical JSON argv signature; Pattern may match the tool name,
// executable basename, or canonical executable path. ArgPattern, when present,
// is matched as a regular expression against argv excluding argv[0].
type ExecAllowlistEntry struct {
	Pattern    string `json:"pattern,omitempty"`
	ArgPattern string `json:"arg_pattern,omitempty"`
	Signature  string `json:"signature,omitempty"`
}

// EffectiveExecPolicy is the concrete, merged caller + execution-host policy.
type EffectiveExecPolicy struct {
	Security    ExecSecurity         `json:"security"`
	Ask         ExecAsk              `json:"ask"`
	AskFallback ExecSecurity         `json:"ask_fallback"`
	TimeoutMS   int                  `json:"timeout_ms"`
	AutoReview  bool                 `json:"auto_review,omitempty"`
	Allowlist   []ExecAllowlistEntry `json:"allowlist,omitempty"`
	Fingerprint string               `json:"fingerprint"`
}

// ExecPolicyRequest is the complete policy-facing execution description.
type ExecPolicyRequest struct {
	Tool             string
	AgentID          string
	Argv             []string
	ExecutablePath   string
	Signature        string
	AllowAlwaysSafe  bool
	Permission       Behavior
	PromptAvailable  bool
	DefaultTimeoutMS int
}

// ExecPolicyDecision is the authoritative runtime result.
type ExecPolicyDecision struct {
	Behavior         Behavior            `json:"behavior"`
	Reason           string              `json:"reason"`
	AllowlistMatched bool                `json:"allowlist_matched"`
	LegacySignature  bool                `json:"legacy_signature"`
	FallbackApplied  bool                `json:"fallback_applied"`
	Effective        EffectiveExecPolicy `json:"effective"`
}

type execPolicyLayer struct {
	security   ExecSecurity
	ask        ExecAsk
	fallback   ExecSecurity
	timeoutMS  int
	autoReview bool
	applies    bool
	allowlist  []ExecAllowlistEntry
	alwaysSigs []string
}

// EvaluateExecPolicy parses and merges caller-requested policy with policy on
// the execution host. Both layers may only tighten execution. An invalid policy
// is an error and callers must deny execution.
func EvaluateExecPolicy(callerPolicy, hostPolicy map[string]any, req ExecPolicyRequest) (ExecPolicyDecision, error) {
	if req.DefaultTimeoutMS <= 0 {
		req.DefaultTimeoutMS = 5 * 60 * 1000
	}
	caller, err := parseExecPolicyLayer(callerPolicy, req.AgentID, req.Tool, req.DefaultTimeoutMS)
	if err != nil {
		return ExecPolicyDecision{}, fmt.Errorf("caller exec policy: %w", err)
	}
	host, err := parseExecPolicyLayer(hostPolicy, req.AgentID, req.Tool, req.DefaultTimeoutMS)
	if err != nil {
		return ExecPolicyDecision{}, fmt.Errorf("execution-host exec policy: %w", err)
	}

	effective := EffectiveExecPolicy{
		Security:    stricterSecurity(caller.security, host.security),
		Ask:         stricterAsk(caller.ask, host.ask),
		AskFallback: stricterSecurity(caller.fallback, host.fallback),
		TimeoutMS:   minPositive(caller.timeoutMS, host.timeoutMS, req.DefaultTimeoutMS),
		AutoReview:  caller.autoReview || host.autoReview,
		Allowlist:   append(append([]ExecAllowlistEntry(nil), caller.allowlist...), host.allowlist...),
	}
	effective.AskFallback = stricterSecurity(effective.Security, effective.AskFallback)
	effective.Fingerprint = fingerprintExecPolicy(effective)

	legacyMatch := req.AllowAlwaysSafe && signatureIn(req.Signature, caller.alwaysSigs, host.alwaysSigs)
	callerMatch := !caller.applies || caller.security == ExecSecurityFull || matchExecAllowlist(caller.allowlist, req)
	hostMatch := !host.applies || host.security == ExecSecurityFull || matchExecAllowlist(host.allowlist, req)
	allowlistMatch := legacyMatch || (callerMatch && hostMatch)

	decision := ExecPolicyDecision{Effective: effective, AllowlistMatched: allowlistMatch, LegacySignature: legacyMatch}
	switch {
	case req.Permission == BehaviorDeny:
		decision.Behavior, decision.Reason = BehaviorDeny, "denied by tool permission policy"
	case effective.Security == ExecSecurityDeny:
		decision.Behavior, decision.Reason = BehaviorDeny, "effective exec security is deny"
	case effective.Ask == ExecAskAlways:
		decision.Behavior, decision.Reason = BehaviorAsk, "effective exec ask policy is always"
	case effective.Security == ExecSecurityFull:
		decision.Behavior, decision.Reason = BehaviorAllow, "effective exec security is full"
	case allowlistMatch:
		decision.Behavior, decision.Reason = BehaviorAllow, "execution matched effective allowlist"
	case effective.Ask == ExecAskOnMiss || req.Permission == BehaviorAsk:
		decision.Behavior, decision.Reason = BehaviorAsk, "execution missed effective allowlist"
	default:
		decision.Behavior, decision.Reason = BehaviorDeny, "execution missed allowlist and prompting is disabled"
	}
	if decision.Behavior == BehaviorAsk && !req.PromptAvailable {
		return ApplyExecAskFallback(decision), nil
	}
	return decision, nil
}

// ApplyExecAskFallback resolves a required prompt that could not produce a
// decision. Explicit reviewer denials and context cancellation must not call it.
func ApplyExecAskFallback(decision ExecPolicyDecision) ExecPolicyDecision {
	decision.FallbackApplied = true
	switch decision.Effective.AskFallback {
	case ExecSecurityFull:
		decision.Behavior, decision.Reason = BehaviorAllow, "exec approval unavailable; full fallback permits execution"
	case ExecSecurityAllowlist:
		if decision.AllowlistMatched {
			decision.Behavior, decision.Reason = BehaviorAllow, "exec approval unavailable; allowlist fallback matched"
		} else {
			decision.Behavior, decision.Reason = BehaviorDeny, "exec approval unavailable; allowlist fallback missed"
		}
	default:
		decision.Behavior, decision.Reason = BehaviorDeny, "exec approval unavailable; deny fallback"
	}
	return decision
}

func parseExecPolicyLayer(raw map[string]any, agentID, tool string, defaultTimeout int) (execPolicyLayer, error) {
	flat, err := flattenExecPolicy(raw, agentID)
	if err != nil {
		return execPolicyLayer{}, err
	}
	layer := execPolicyLayer{security: ExecSecurityFull, ask: ExecAskOff, fallback: ExecSecurityDeny, timeoutMS: defaultTimeout}
	if len(flat) == 0 {
		return layer, nil
	}

	mode, _ := flat["mode"].(string)
	mode = strings.ToLower(strings.TrimSpace(mode))
	if _, exists := flat["mode"]; exists {
		switch mode {
		case "deny":
			layer.security, layer.ask = ExecSecurityDeny, ExecAskOff
		case "allowlist":
			layer.security, layer.ask = ExecSecurityAllowlist, ExecAskOff
		case "ask":
			layer.security, layer.ask = ExecSecurityAllowlist, ExecAskOnMiss
		case "auto":
			layer.security, layer.ask, layer.autoReview = ExecSecurityAllowlist, ExecAskOnMiss, true
		case "full":
			layer.security, layer.ask = ExecSecurityFull, ExecAskOff
		case "allow": // shipped compatibility value
			layer.security, layer.ask = ExecSecurityFull, ExecAskOff
		default:
			return layer, fmt.Errorf("invalid mode %q", mode)
		}
	}

	if value, exists := flat["security"]; exists {
		security, err := parseExecSecurity(value)
		if err != nil {
			return layer, err
		}
		layer.security = security
	}
	if value, exists := flat["ask"]; exists {
		ask, err := parseExecAsk(value)
		if err != nil {
			return layer, err
		}
		layer.ask = ask
	}
	fallbackValue, fallbackExists := flat["askFallback"]
	if !fallbackExists {
		fallbackValue, fallbackExists = flat["ask_fallback"]
	}
	if fallbackExists {
		fallback, err := parseExecSecurity(fallbackValue)
		if err != nil {
			return layer, fmt.Errorf("askFallback: %w", err)
		}
		layer.fallback = fallback
	}
	if value, exists := flat["timeout_ms"]; exists {
		n, ok := positivePolicyInt(value)
		if !ok {
			return layer, fmt.Errorf("timeout_ms must be a positive integer")
		}
		layer.timeoutMS = n
	}

	tools, toolsPresent, err := policyStringSlice(flat["tools"])
	if err != nil {
		return layer, fmt.Errorf("tools: %w", err)
	}
	layer.applies = !toolsPresent || matchPolicyPatternList(tools, tool)
	if toolsPresent && mode == "" {
		// Historical tools lists meant "ask for these tools". Making the field
		// live must preserve that behavior instead of silently opening execution.
		layer.security, layer.ask = ExecSecurityAllowlist, ExecAskOnMiss
	}

	layer.allowlist, err = parseExecAllowlist(flat["allowlist"])
	if err != nil {
		return layer, err
	}
	if len(layer.allowlist) > 0 && mode == "" {
		if _, hasSecurity := flat["security"]; !hasSecurity {
			layer.security = ExecSecurityAllowlist
		}
	}
	layer.alwaysSigs, _, err = policyStringSlice(flat["allow_always_signatures"])
	if err != nil {
		return layer, fmt.Errorf("allow_always_signatures: %w", err)
	}
	if !layer.applies {
		layer.security, layer.ask, layer.fallback = ExecSecurityFull, ExecAskOff, ExecSecurityFull
		layer.allowlist, layer.alwaysSigs = nil, nil
	}
	return layer, nil
}

func flattenExecPolicy(raw map[string]any, agentID string) (map[string]any, error) {
	out := map[string]any{}
	mergePolicyMap(out, raw)
	delete(out, "defaults")
	delete(out, "agents")
	if defaults, exists := raw["defaults"]; exists {
		m, ok := asPolicyMap(defaults)
		if !ok {
			return nil, fmt.Errorf("defaults must be an object")
		}
		mergePolicyMap(out, m)
	}
	if agentsValue, exists := raw["agents"]; exists {
		agents, ok := asPolicyMap(agentsValue)
		if !ok {
			return nil, fmt.Errorf("agents must be an object")
		}
		if wildcard, ok := asPolicyMap(agents["*"]); ok {
			mergePolicyMap(out, wildcard)
		}
		if exact, ok := asPolicyMap(agents[agentID]); ok && agentID != "" {
			mergePolicyMap(out, exact)
		}
	}
	return out, nil
}

func mergePolicyMap(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

func asPolicyMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func parseExecSecurity(v any) (ExecSecurity, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("security must be a string")
	}
	switch ExecSecurity(strings.ToLower(strings.TrimSpace(s))) {
	case ExecSecurityDeny:
		return ExecSecurityDeny, nil
	case ExecSecurityAllowlist:
		return ExecSecurityAllowlist, nil
	case ExecSecurityFull:
		return ExecSecurityFull, nil
	default:
		return "", fmt.Errorf("invalid security %q", s)
	}
}

func parseExecAsk(v any) (ExecAsk, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("ask must be a string")
	}
	switch ExecAsk(strings.ToLower(strings.TrimSpace(s))) {
	case ExecAskOff:
		return ExecAskOff, nil
	case ExecAskOnMiss:
		return ExecAskOnMiss, nil
	case ExecAskAlways:
		return ExecAskAlways, nil
	default:
		return "", fmt.Errorf("invalid ask %q", s)
	}
}

func parseExecAllowlist(raw any) ([]ExecAllowlistEntry, error) {
	if raw == nil {
		return nil, nil
	}
	values, ok := raw.([]any)
	if !ok {
		if stringsValues, ok := raw.([]string); ok {
			values = make([]any, len(stringsValues))
			for i := range stringsValues {
				values[i] = stringsValues[i]
			}
		} else {
			return nil, fmt.Errorf("allowlist must be an array")
		}
	}
	out := make([]ExecAllowlistEntry, 0, len(values))
	for i, value := range values {
		switch item := value.(type) {
		case string:
			item = strings.TrimSpace(item)
			if item == "" {
				return nil, fmt.Errorf("allowlist[%d] is empty", i)
			}
			if isCanonicalExecSignature(item) {
				out = append(out, ExecAllowlistEntry{Signature: item})
			} else {
				out = append(out, ExecAllowlistEntry{Pattern: item})
			}
		case map[string]any:
			pattern, _ := item["pattern"].(string)
			argPattern, _ := item["argPattern"].(string)
			if argPattern == "" {
				argPattern, _ = item["arg_pattern"].(string)
			}
			pattern, argPattern = strings.TrimSpace(pattern), strings.TrimSpace(argPattern)
			if pattern == "" {
				return nil, fmt.Errorf("allowlist[%d].pattern is required", i)
			}
			if argPattern != "" {
				if _, err := regexp.Compile(argPattern); err != nil {
					return nil, fmt.Errorf("allowlist[%d].argPattern is invalid: %w", i, err)
				}
			}
			out = append(out, ExecAllowlistEntry{Pattern: pattern, ArgPattern: argPattern})
		default:
			return nil, fmt.Errorf("allowlist[%d] must be a string or object", i)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		left, _ := json.Marshal(out[i])
		right, _ := json.Marshal(out[j])
		return string(left) < string(right)
	})
	return out, nil
}

func matchExecAllowlist(entries []ExecAllowlistEntry, req ExecPolicyRequest) bool {
	argumentText := ""
	if len(req.Argv) > 1 {
		argumentText = strings.Join(req.Argv[1:], " ")
	}
	for _, entry := range entries {
		if entry.Signature != "" && entry.Signature == req.Signature {
			return true
		}
		if entry.Pattern == "" {
			continue
		}
		pathMatched := false
		argvExecutable := ""
		if len(req.Argv) > 0 {
			argvExecutable = req.Argv[0]
		}
		for _, candidate := range []string{req.Tool, filepath.Base(argvExecutable), argvExecutable, filepath.Base(req.ExecutablePath), req.ExecutablePath} {
			if candidate == "" {
				continue
			}
			if entry.Pattern == candidate {
				pathMatched = true
				break
			}
			if matched, err := filepath.Match(entry.Pattern, candidate); err == nil && matched {
				pathMatched = true
				break
			}
		}
		if !pathMatched {
			continue
		}
		if entry.ArgPattern == "" {
			return true
		}
		if matched, err := regexp.MatchString(entry.ArgPattern, argumentText); err == nil && matched {
			return true
		}
	}
	return false
}

func matchPolicyPatternList(patterns []string, value string) bool {
	for _, pattern := range patterns {
		if pattern == value {
			return true
		}
		if matched, err := filepath.Match(pattern, value); err == nil && matched {
			return true
		}
	}
	return false
}

func policyStringSlice(raw any) ([]string, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	var values []string
	switch v := raw.(type) {
	case string:
		for _, item := range strings.Split(v, ",") {
			if item = strings.TrimSpace(item); item != "" {
				values = append(values, item)
			}
		}
	case []string:
		values = append(values, v...)
	case []any:
		for i, item := range v {
			s, ok := item.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return nil, true, fmt.Errorf("entry %d must be a non-empty string", i)
			}
			values = append(values, strings.TrimSpace(s))
		}
	default:
		return nil, true, fmt.Errorf("must be a string or array of strings")
	}
	return values, true, nil
}

func positivePolicyInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		if n > 0 {
			return n, true
		}
	case int64:
		if n > 0 && n <= int64(^uint(0)>>1) {
			return int(n), true
		}
	case float64:
		if n > 0 && n == float64(int(n)) {
			return int(n), true
		}
	case json.Number:
		i, err := n.Int64()
		if err == nil && i > 0 {
			return int(i), true
		}
	}
	return 0, false
}

func stricterSecurity(a, b ExecSecurity) ExecSecurity {
	rank := map[ExecSecurity]int{ExecSecurityDeny: 0, ExecSecurityAllowlist: 1, ExecSecurityFull: 2}
	if rank[a] <= rank[b] {
		return a
	}
	return b
}

func stricterAsk(a, b ExecAsk) ExecAsk {
	rank := map[ExecAsk]int{ExecAskOff: 0, ExecAskOnMiss: 1, ExecAskAlways: 2}
	if rank[a] >= rank[b] {
		return a
	}
	return b
}

func minPositive(values ...int) int {
	out := 0
	for _, value := range values {
		if value > 0 && (out == 0 || value < out) {
			out = value
		}
	}
	return out
}

func signatureIn(signature string, sets ...[]string) bool {
	if signature == "" {
		return false
	}
	for _, set := range sets {
		for _, value := range set {
			if value == signature {
				return true
			}
		}
	}
	return false
}

func isCanonicalExecSignature(value string) bool {
	var parts []string
	return json.Unmarshal([]byte(value), &parts) == nil && len(parts) >= 2 && parts[0] == "exec"
}

func fingerprintExecPolicy(policy EffectiveExecPolicy) string {
	policy.Fingerprint = ""
	data, _ := json.Marshal(policy)
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
