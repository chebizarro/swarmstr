package main

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ─── flag types ───────────────────────────────────────────────────────────────

type csvListFlag []string

func (f *csvListFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *csvListFlag) Set(value string) error {
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		*f = append(*f, part)
	}
	return nil
}

type stringListFlag []string

func (f stringListFlag) Values() []string {
	out := make([]string, 0, len(f))
	for _, value := range f {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func (f *stringListFlag) String() string {
	if f == nil {
		return ""
	}
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type keyValueFlag map[string]string

func (f *keyValueFlag) String() string {
	if f == nil || *f == nil {
		return ""
	}
	keys := make([]string, 0, len(*f))
	for key := range *f {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+(*f)[key])
	}
	return strings.Join(parts, ",")
}

func (f *keyValueFlag) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	if !ok || key == "" {
		return fmt.Errorf("expected KEY=VALUE")
	}
	if *f == nil {
		*f = map[string]string{}
	}
	(*f)[key] = val
	return nil
}

// ─── observe helpers ──────────────────────────────────────────────────────────

func parseObserveWait(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if digitsOnly(raw) {
		ms, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("invalid --wait value %q: %w", raw, err)
		}
		if ms < 0 {
			return 0, fmt.Errorf("--wait must be >= 0")
		}
		return ms, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid --wait duration %q: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("--wait must be >= 0")
	}
	return int(d.Milliseconds()), nil
}

func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ─── shared field accessors ──────────────────────────────────────────────────

// stringFieldAny extracts a string from map[string]any.
func stringFieldAny(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func boolFieldAny(m map[string]any, key string) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// floatFieldAny extracts a float64 from map[string]any.
func floatFieldAny(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func intFieldAny(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func anySlice(v any) []any {
	items, _ := v.([]any)
	return items
}

func stringSliceAny(v any) []string {
	switch raw := v.(type) {
	case []string:
		return append([]string(nil), raw...)
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func promptText(in io.Reader, out io.Writer, label, def string, secret bool) (string, error) {
	if out == nil {
		out = io.Discard
	}
	if in == nil {
		in = strings.NewReader("")
	}
	prompt := label
	if strings.TrimSpace(def) != "" {
		prompt = fmt.Sprintf("%s [%s]", label, def)
	}
	if secret {
		prompt += " (input hidden in shell history only if your terminal supports it)"
	}
	fmt.Fprintf(out, "%s: ", prompt)
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		value = strings.TrimSpace(def)
	}
	return value, nil
}

func promptConfirm(in io.Reader, out io.Writer, label string, def bool) (bool, error) {
	defText := "y"
	if !def {
		defText = "n"
	}
	value, err := promptText(in, out, label+" (y/n)", defText, false)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "y", "yes", "true", "1", "on":
		return true, nil
	case "n", "no", "false", "0", "off":
		return false, nil
	default:
		return false, fmt.Errorf("expected yes or no")
	}
}

func channelStatusLabel(m map[string]any) string {
	if status := stringFieldAny(m, "status"); status != "" {
		return status
	}
	if boolFieldAny(m, "logged_out") {
		return "logged_out"
	}
	if boolFieldAny(m, "connected") {
		return "connected"
	}
	return "disconnected"
}
