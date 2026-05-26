package toolloop

import (
	"fmt"
	"strings"
)

type CompactionConfig struct {
	Enabled           bool
	DefaultMaxBytes   int
	FirstLines        int
	LastLines         int
	ErrorContextLines int
	PerToolMaxBytes   map[string]int
}

type CompactionResult struct {
	ToolName            string
	OriginalBytes       int
	CompactedBytes      int
	Compacted           bool
	Output              string
	Notice              string
	PreservedErrorLines int
}

func DefaultCompactionConfig() CompactionConfig {
	return CompactionConfig{Enabled: true, DefaultMaxBytes: 12 * 1024, FirstLines: 80, LastLines: 80, ErrorContextLines: 3, PerToolMaxBytes: map[string]int{"bash": 8 * 1024, "exec": 8 * 1024, "file_search": 16 * 1024, "read_file": 20 * 1024}}
}

func applyCompactionDefaults(cfg CompactionConfig) CompactionConfig {
	def := DefaultCompactionConfig()
	usedDefaultBudget := false
	if cfg.DefaultMaxBytes <= 0 {
		cfg.DefaultMaxBytes = def.DefaultMaxBytes
		usedDefaultBudget = true
	}
	if cfg.FirstLines <= 0 {
		cfg.FirstLines = def.FirstLines
	}
	if cfg.LastLines <= 0 {
		cfg.LastLines = def.LastLines
	}
	if cfg.ErrorContextLines < 0 {
		cfg.ErrorContextLines = def.ErrorContextLines
	}
	if cfg.PerToolMaxBytes == nil && usedDefaultBudget {
		cfg.PerToolMaxBytes = def.PerToolMaxBytes
	}
	return cfg
}

func CompactToolOutput(toolName, output string, cfg CompactionConfig) CompactionResult {
	if !cfg.Enabled {
		return CompactionResult{ToolName: toolName, OriginalBytes: len(output), CompactedBytes: len(output), Output: output}
	}
	cfg = applyCompactionDefaults(cfg)
	maxBytes := cfg.DefaultMaxBytes
	if cfg.PerToolMaxBytes != nil {
		if v := cfg.PerToolMaxBytes[toolName]; v > 0 {
			maxBytes = v
		}
	}
	res := CompactionResult{ToolName: toolName, OriginalBytes: len(output), CompactedBytes: len(output), Output: output}
	if len(output) <= maxBytes {
		return res
	}
	lines := strings.Split(output, "\n")
	firstN, lastN := cfg.FirstLines, cfg.LastLines
	if firstN <= 0 {
		firstN = 40
	}
	if lastN <= 0 {
		lastN = 40
	}
	var kept []string
	kept = append(kept, fmt.Sprintf("[tokenjuice: compacted %s output from %d bytes to fit %d-byte budget]", toolName, len(output), maxBytes))
	kept = append(kept, "--- first lines ---")
	kept = append(kept, takeFirst(lines, firstN)...)
	errLines := errorSections(lines, cfg.ErrorContextLines)
	if len(errLines) > 0 {
		kept = append(kept, "--- error/warning context ---")
		kept = append(kept, errLines...)
		res.PreservedErrorLines = len(errLines)
	}
	key := keyLines(lines)
	if len(key) > 0 {
		kept = append(kept, "--- key output ---")
		kept = append(kept, key...)
	}
	kept = append(kept, "--- last lines ---")
	kept = append(kept, takeLast(lines, lastN)...)
	compacted := strings.Join(dedupePreserveOrder(kept), "\n")
	if len(compacted) > maxBytes {
		compacted = trimMiddle(compacted, maxBytes)
	}
	res.Output = compacted
	res.Compacted = true
	res.CompactedBytes = len(compacted)
	res.Notice = fmt.Sprintf("tokenjuice compacted %d -> %d bytes", res.OriginalBytes, res.CompactedBytes)
	return res
}

func takeFirst(lines []string, n int) []string {
	if n > len(lines) {
		n = len(lines)
	}
	return append([]string(nil), lines[:n]...)
}
func takeLast(lines []string, n int) []string {
	if n > len(lines) {
		n = len(lines)
	}
	return append([]string(nil), lines[len(lines)-n:]...)
}

func errorSections(lines []string, ctx int) []string {
	if ctx < 0 {
		ctx = 0
	}
	seen := map[int]bool{}
	for i, l := range lines {
		low := strings.ToLower(l)
		if strings.Contains(low, "error") || strings.Contains(low, "failed") || strings.Contains(low, "panic") || strings.Contains(low, "warning") || strings.Contains(low, "exception") {
			start, end := i-ctx, i+ctx
			if start < 0 {
				start = 0
			}
			if end >= len(lines) {
				end = len(lines) - 1
			}
			for j := start; j <= end; j++ {
				seen[j] = true
			}
		}
	}
	var out []string
	for i, l := range lines {
		if seen[i] {
			out = append(out, l)
		}
	}
	return out
}

func keyLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		low := strings.ToLower(strings.TrimSpace(l))
		if strings.HasPrefix(low, "summary:") || strings.HasPrefix(low, "result:") || strings.HasPrefix(low, "total") || strings.Contains(low, "event_id=") || strings.Contains(low, "commit ") {
			out = append(out, l)
			if len(out) >= 40 {
				break
			}
		}
	}
	return out
}

func dedupePreserveOrder(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		key := strings.TrimSpace(s)
		if key == "" {
			out = append(out, s)
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func trimMiddle(s string, max int) string {
	if max <= 128 || len(s) <= max {
		return s
	}
	notice := fmt.Sprintf("\n...[tokenjuice trimmed %d additional bytes]...\n", len(s)-max)
	head := (max - len(notice)) / 2
	tail := max - len(notice) - head
	if head < 0 || tail < 0 {
		return s[:max]
	}
	return s[:head] + notice + s[len(s)-tail:]
}
