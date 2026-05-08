package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MemoryEvalBaselineSchemaVersion = 1
	MemoryEvalDatasetVersion        = "synthetic-v1"
)

type MemoryEvalBaseline struct {
	SchemaVersion  int           `json:"schema_version"`
	DatasetVersion string        `json:"dataset_version"`
	RecordedAt     string        `json:"recorded_at"`
	Run            MemoryEvalRun `json:"run"`
}

type MemoryEvalRegression struct {
	Failures []string `json:"failures,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func NewMemoryEvalBaseline(run MemoryEvalRun) MemoryEvalBaseline {
	return MemoryEvalBaseline{
		SchemaVersion:  MemoryEvalBaselineSchemaVersion,
		DatasetVersion: MemoryEvalDatasetVersion,
		RecordedAt:     time.Now().UTC().Format(time.RFC3339),
		Run:            run,
	}
}

func SaveMemoryEvalBaseline(rootDir string, baseline MemoryEvalBaseline, now time.Time) (string, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	path := filepath.Join(rootDir, now.Format("2006-01-02")+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	payload, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func LoadMemoryEvalBaseline(path string) (MemoryEvalBaseline, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return MemoryEvalBaseline{}, err
	}
	var b MemoryEvalBaseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return MemoryEvalBaseline{}, err
	}
	return b, nil
}

func CompareMemoryEvalRuns(baseline, current MemoryEvalRun) MemoryEvalRegression {
	out := MemoryEvalRegression{}
	if baseline.RecallAt5 > 0 {
		drop := (baseline.RecallAt5 - current.RecallAt5) / baseline.RecallAt5
		if drop > 0.10 {
			out.Failures = append(out.Failures, fmt.Sprintf("recall@5 dropped %.1f%%", drop*100))
		} else if drop > 0.05 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("recall@5 dropped %.1f%%", drop*100))
		}
	}
	if baseline.P95LatencyMS > 0 {
		inc := (current.P95LatencyMS - baseline.P95LatencyMS) / baseline.P95LatencyMS
		if inc > 0.50 {
			out.Failures = append(out.Failures, fmt.Sprintf("p95 latency increased %.1f%%", inc*100))
		} else if inc > 0.05 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("p95 latency increased %.1f%%", inc*100))
		}
	}
	warnIfRegression := func(label string, base, now float64, higherIsWorse bool) {
		if base <= 0 {
			return
		}
		var reg float64
		if higherIsWorse {
			reg = (now - base) / base
		} else {
			reg = (base - now) / base
		}
		if reg > 0.05 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("%s regressed %.1f%%", label, reg*100))
		}
	}
	warnIfRegression("recall@10", baseline.RecallAt10, current.RecallAt10, false)
	warnIfRegression("no_result_rate", baseline.NoResultRate, current.NoResultRate, true)
	warnIfRegression("stale_hit_rate", baseline.StaleHitRate, current.StaleHitRate, true)
	warnIfRegression("superseded_hit_rate", baseline.SupersededHitRate, current.SupersededHitRate, true)
	warnIfRegression("duplicate_hit_rate", baseline.DuplicateRate, current.DuplicateRate, true)
	warnIfRegression("p50_latency_ms", baseline.P50LatencyMS, current.P50LatencyMS, true)
	warnIfRegression("p99_latency_ms", baseline.P99LatencyMS, current.P99LatencyMS, true)
	warnIfRegression("reflection_precision", baseline.ReflectionPrecision, current.ReflectionPrecision, false)
	warnIfRegression("promotion_acceptance", baseline.PromotionAcceptance, current.PromotionAcceptance, false)
	if baseline.TokenCost > 0 {
		reg := float64(current.TokenCost-baseline.TokenCost) / float64(baseline.TokenCost)
		if reg > 0.05 {
			out.Warnings = append(out.Warnings, fmt.Sprintf("token_cost increased %.1f%%", reg*100))
		}
	}
	return out
}

func BaselineDir(home string) string {
	return filepath.Join(strings.TrimSpace(home), ".metiq", "memory-evals", "baselines")
}
