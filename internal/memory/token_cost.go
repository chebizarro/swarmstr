package memory

import (
	"math"
	"sort"
	"strings"
)

const (
	memoryTokenEstimateMultiplier = 1.3
	memoryTokenAlertThreshold     = 8000
)

func EstimateTokenCostText(text string) int {
	words := len(strings.Fields(strings.TrimSpace(text)))
	if words <= 0 {
		return 0
	}
	return int(math.Ceil(float64(words) * memoryTokenEstimateMultiplier))
}

func EstimateMemoryCardsTokenCost(cards []MemoryCard) int {
	total := 0
	for _, c := range cards {
		total += EstimateTokenCostText(c.Text)
		total += EstimateTokenCostText(c.Summary)
	}
	return total
}

func trimCardsToTokenBudget(cards []MemoryCard, budget int) ([]MemoryCard, int) {
	if budget <= 0 || len(cards) == 0 {
		return cards, EstimateMemoryCardsTokenCost(cards)
	}
	used := 0
	trimmed := make([]MemoryCard, 0, len(cards))
	for _, card := range cards {
		cost := EstimateTokenCostText(card.Text) + EstimateTokenCostText(card.Summary)
		if len(trimmed) > 0 && used+cost > budget {
			break
		}
		trimmed = append(trimmed, card)
		used += cost
		if used >= budget {
			break
		}
	}
	if len(trimmed) == 0 {
		trimmed = cards[:1]
		used = EstimateTokenCostText(trimmed[0].Text) + EstimateTokenCostText(trimmed[0].Summary)
	}
	return trimmed, used
}

func percentileInts(values []int, p float64) int {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int(nil), values...)
	sort.Ints(sorted)
	if p <= 0 {
		return sorted[0]
	}
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
