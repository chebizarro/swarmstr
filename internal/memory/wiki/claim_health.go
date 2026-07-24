package wiki

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Freshness thresholds, in days, matching the OpenClaw memory-wiki claim
// health lifecycle: claims age at 30 days and go stale at 90.
const (
	ClaimAgingDays = 30
	ClaimStaleDays = 90
)

// Freshness levels for compiled claims.
const (
	ClaimFreshnessFresh   = "fresh"
	ClaimFreshnessAging   = "aging"
	ClaimFreshnessStale   = "stale"
	ClaimFreshnessUnknown = "unknown"
)

var contestedClaimStatuses = map[string]struct{}{
	"contested":    {},
	"contradicted": {},
	"refuted":      {},
	"superseded":   {},
}

// ClaimFreshness classifies how recently a claim's backing page was touched.
type ClaimFreshness struct {
	Level          string `json:"level"`
	Reason         string `json:"reason"`
	DaysSinceTouch int    `json:"days_since_touch"`
	LastTouchedAt  string `json:"last_touched_at,omitempty"`
}

// ClaimHealth is the per-claim health record derived from the compiled cache.
type ClaimHealth struct {
	Key             string         `json:"key"`
	PagePath        string         `json:"page_path"`
	PageTitle       string         `json:"page_title,omitempty"`
	PageID          string         `json:"page_id,omitempty"`
	ClaimID         string         `json:"claim_id"`
	Text            string         `json:"text"`
	Status          string         `json:"status"`
	Confidence      float64        `json:"confidence,omitempty"`
	EvidenceCount   int            `json:"evidence_count"`
	MissingEvidence bool           `json:"missing_evidence"`
	Freshness       ClaimFreshness `json:"freshness"`
}

// ClaimContradictionCluster groups claims whose normalized text matches but
// whose recorded statuses diverge across the vault.
type ClaimContradictionCluster struct {
	Key     string        `json:"key"`
	Label   string        `json:"label"`
	Entries []ClaimHealth `json:"entries"`
}

// ClaimHealthReport is the aggregate claim lifecycle surface for a vault.
type ClaimHealthReport struct {
	Generation      string                      `json:"generation"`
	GeneratedAt     time.Time                   `json:"generated_at"`
	TotalClaims     int                         `json:"total_claims"`
	FreshnessCounts map[string]int              `json:"freshness_counts"`
	StaleClaims     []ClaimHealth               `json:"stale_claims,omitempty"`
	ContestedClaims []ClaimHealth               `json:"contested_claims,omitempty"`
	MissingEvidence []ClaimHealth               `json:"missing_evidence,omitempty"`
	Contradictions  []ClaimContradictionCluster `json:"contradictions,omitempty"`
}

// NormalizeClaimStatus lowercases and trims a claim status, defaulting to
// "supported" when blank.
func NormalizeClaimStatus(status string) string {
	normalized := strings.ToLower(strings.TrimSpace(status))
	if normalized == "" {
		return "supported"
	}
	return normalized
}

// IsContestedClaimStatus reports whether a status marks a claim as contested.
func IsContestedClaimStatus(status string) bool {
	_, ok := contestedClaimStatuses[NormalizeClaimStatus(status)]
	return ok
}

// AssessClaimFreshness classifies a claim timestamp relative to now. Blank or
// unparseable timestamps yield the "unknown" level.
func AssessClaimFreshness(updatedAt string, now time.Time) ClaimFreshness {
	trimmed := strings.TrimSpace(updatedAt)
	if trimmed == "" {
		return ClaimFreshness{Level: ClaimFreshnessUnknown, Reason: "missing updated_at"}
	}
	touched, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return ClaimFreshness{Level: ClaimFreshnessUnknown, Reason: "invalid updated_at " + trimmed}
	}
	days := int(now.Sub(touched).Hours() / 24)
	if days < 0 {
		days = 0
	}
	fresh := ClaimFreshness{Reason: "last touched " + trimmed, DaysSinceTouch: days, LastTouchedAt: trimmed}
	switch {
	case days >= ClaimStaleDays:
		fresh.Level = ClaimFreshnessStale
	case days >= ClaimAgingDays:
		fresh.Level = ClaimFreshnessAging
	default:
		fresh.Level = ClaimFreshnessFresh
	}
	return fresh
}

// CollectClaimHealth derives per-claim health records from a compiled cache in
// deterministic (cache) order.
func CollectClaimHealth(cache CompiledCache, now time.Time) []ClaimHealth {
	pagesByID := make(map[string]CompiledPage, len(cache.Pages))
	for _, page := range cache.Pages {
		pagesByID[page.ID] = page
	}
	out := make([]ClaimHealth, 0, len(cache.Claims))
	for _, claim := range cache.Claims {
		page := pagesByID[claim.PageID]
		timestamp := firstNonBlank(claim.UpdatedAt, page.UpdatedAt)
		out = append(out, ClaimHealth{
			Key:             claim.PagePath + "#" + claim.ID,
			PagePath:        claim.PagePath,
			PageTitle:       page.Title,
			PageID:          claim.PageID,
			ClaimID:         claim.ID,
			Text:            claim.Text,
			Status:          NormalizeClaimStatus(claim.Status),
			Confidence:      claim.Confidence,
			EvidenceCount:   len(claim.SourceIDs),
			MissingEvidence: len(claim.SourceIDs) == 0,
			Freshness:       AssessClaimFreshness(timestamp, now),
		})
	}
	return out
}

// BuildClaimContradictionClusters detects claims whose normalized text is
// identical while their statuses diverge (for example one page supports what
// another page refutes). Clusters and entries are deterministically sorted.
func BuildClaimContradictionClusters(cache CompiledCache, now time.Time) []ClaimContradictionCluster {
	health := CollectClaimHealth(cache, now)
	byText := map[string][]ClaimHealth{}
	for _, claim := range health {
		key := normalizeClaimTextKey(claim.Text)
		if key == "" {
			continue
		}
		byText[key] = append(byText[key], claim)
	}
	clusters := make([]ClaimContradictionCluster, 0)
	for key, entries := range byText {
		if len(entries) < 2 {
			continue
		}
		statuses := map[string]struct{}{}
		for _, entry := range entries {
			statuses[entry.Status] = struct{}{}
		}
		if len(statuses) < 2 {
			continue
		}
		sorted := append([]ClaimHealth(nil), entries...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].PagePath != sorted[j].PagePath {
				return sorted[i].PagePath < sorted[j].PagePath
			}
			return sorted[i].ClaimID < sorted[j].ClaimID
		})
		clusters = append(clusters, ClaimContradictionCluster{Key: key, Label: sorted[0].Text, Entries: sorted})
	}
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].Label != clusters[j].Label {
			return clusters[i].Label < clusters[j].Label
		}
		return clusters[i].Key < clusters[j].Key
	})
	return clusters
}

// BuildClaimHealthReport aggregates freshness, contested-status, evidence, and
// contradiction signals into one deterministic health surface.
func BuildClaimHealthReport(cache CompiledCache, now time.Time) ClaimHealthReport {
	health := CollectClaimHealth(cache, now)
	report := ClaimHealthReport{
		Generation:  cache.Generation,
		GeneratedAt: now,
		TotalClaims: len(health),
		FreshnessCounts: map[string]int{
			ClaimFreshnessFresh:   0,
			ClaimFreshnessAging:   0,
			ClaimFreshnessStale:   0,
			ClaimFreshnessUnknown: 0,
		},
	}
	for _, claim := range health {
		report.FreshnessCounts[claim.Freshness.Level]++
		if claim.Freshness.Level == ClaimFreshnessStale {
			report.StaleClaims = append(report.StaleClaims, claim)
		}
		if IsContestedClaimStatus(claim.Status) {
			report.ContestedClaims = append(report.ContestedClaims, claim)
		}
		if claim.MissingEvidence {
			report.MissingEvidence = append(report.MissingEvidence, claim)
		}
	}
	report.Contradictions = BuildClaimContradictionClusters(cache, now)
	return report
}

// ClaimHealth loads the most recently compiled cache and returns its claim
// health report evaluated at the pipeline clock.
func (p *Pipeline) ClaimHealth(ctx context.Context) (ClaimHealthReport, error) {
	cache, err := p.LoadCompiledCache(ctx)
	if err != nil {
		return ClaimHealthReport{}, err
	}
	return BuildClaimHealthReport(cache, p.now().UTC()), nil
}

// normalizeClaimTextKey collapses a claim's text to a comparison key:
// lowercase, letters/digits only, single-space separated.
func normalizeClaimTextKey(text string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			b.WriteRune(r)
			space = false
		} else if b.Len() > 0 && !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}
