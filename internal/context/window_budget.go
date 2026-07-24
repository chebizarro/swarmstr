package context

import "sort"

type windowProjection struct {
	Messages []Message
	Summary  string
	Tokens   int
}

// fitWindowProjection applies a positive per-assembly budget without mutating
// stored session history. Tool-call assistant messages and their results form
// atomic groups so trimming cannot create new orphaned tool exchanges.
func fitWindowProjection(messages []Message, summary string, maxTokens int) windowProjection {
	projected := append([]Message(nil), messages...)
	if maxTokens <= 0 {
		return windowProjection{Messages: projected, Summary: summary, Tokens: EstimateMessagesTokens(projected) + EstimateTextTokens(summary)}
	}
	latestUser := latestUserIndex(projected)
	groups := linkedMessageGroups(projected)
	keep := make([]bool, len(projected))
	for i := range keep {
		keep[i] = true
	}
	total := EstimateMessagesTokens(projected) + EstimateTextTokens(summary)
	for _, group := range groups {
		if total <= maxTokens || containsIndex(group, latestUser) {
			continue
		}
		for _, idx := range group {
			if keep[idx] {
				total -= EstimateMessageTokens(projected[idx])
				keep[idx] = false
			}
		}
	}
	projected = filterMessages(projected, keep)
	latestUser = latestUserIndex(projected)
	messageTokens := EstimateMessagesTokens(projected)
	if messageTokens+EstimateTextTokens(summary) > maxTokens {
		if latestUser >= 0 && messageTokens > maxTokens {
			// Only the protected latest-user group can remain at this point. Fit its
			// content exactly; metadata is preserved even at a one-token budget.
			projected[latestUser].Content = fitMessageContent(projected[latestUser], maxTokens)
			messageTokens = EstimateMessagesTokens(projected)
		}
		remaining := maxTokens - messageTokens
		if remaining < 0 {
			remaining = 0
		}
		summary = fitTextTokens(summary, remaining)
	}
	total = EstimateMessagesTokens(projected) + EstimateTextTokens(summary)
	// Defensive fallback for malformed tool-heavy input that cannot be shrunk
	// through message content alone.
	for total > maxTokens && len(projected) > 0 {
		if latestUser == 0 && len(projected) == 1 {
			// User messages must not carry tool-call metadata, but discard it here
			// rather than violating the public hard-cap contract on malformed input.
			projected[0].ToolCalls = nil
			projected[0].ToolCallID = ""
			projected[0].Content = fitMessageContent(projected[0], maxTokens)
			total = EstimateMessagesTokens(projected) + EstimateTextTokens(summary)
			break
		}
		projected = projected[1:]
		latestUser = latestUserIndex(projected)
		total = EstimateMessagesTokens(projected) + EstimateTextTokens(summary)
	}
	return windowProjection{Messages: projected, Summary: summary, Tokens: total}
}

func fitRecallToBudget(messages []Message, summary, recall string, maxTokens int) (string, int) {
	messageTokens := EstimateMessagesTokens(messages)
	additionLimit := maxTokens - messageTokens
	if additionLimit <= 0 || recall == "" {
		addition := summary
		return addition, messageTokens + EstimateTextTokens(addition)
	}
	join := func(r string) string {
		if summary == "" {
			return r
		}
		if r == "" {
			return summary
		}
		return summary + "\n\n" + r
	}
	if EstimateTextTokens(join(recall)) > additionLimit {
		lo, hi := 0, len(recall)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			candidate := TruncateUTF8(recall, mid)
			if EstimateTextTokens(join(candidate)) <= additionLimit {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		recall = TruncateUTF8(recall, lo)
	}
	addition := join(recall)
	return addition, messageTokens + EstimateTextTokens(addition)
}

func fitTextTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	if EstimateTextTokens(text) <= maxTokens {
		return text
	}
	lo, hi := 0, len(text)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		candidate := TruncateUTF8(text, mid)
		if EstimateTextTokens(candidate) <= maxTokens {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return TruncateUTF8(text, lo)
}

func fitMessageContent(msg Message, maxTokens int) string {
	if EstimateMessageTokens(msg) <= maxTokens {
		return msg.Content
	}
	lo, hi := 0, len(msg.Content)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		candidate := msg
		candidate.Content = TruncateUTF8(msg.Content, mid)
		if EstimateMessageTokens(candidate) <= maxTokens {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return TruncateUTF8(msg.Content, lo)
}

func latestUserIndex(messages []Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Content != "" {
			return i
		}
	}
	return -1
}

func filterMessages(messages []Message, keep []bool) []Message {
	out := make([]Message, 0, len(messages))
	for i, msg := range messages {
		if keep[i] {
			out = append(out, msg)
		}
	}
	return out
}

func containsIndex(group []int, idx int) bool {
	if idx < 0 {
		return false
	}
	for _, value := range group {
		if value == idx {
			return true
		}
	}
	return false
}

func linkedMessageGroups(messages []Message) [][]int {
	parent := make([]int, len(messages))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b int) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	owners := map[string]int{}
	for i, msg := range messages {
		if msg.Role != "assistant" {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.ID == "" {
				continue
			}
			if owner, ok := owners[call.ID]; ok {
				union(owner, i)
			} else {
				owners[call.ID] = i
			}
		}
	}
	for i, msg := range messages {
		if msg.ToolCallID != "" {
			if owner, ok := owners[msg.ToolCallID]; ok {
				union(owner, i)
			}
		}
	}
	byRoot := map[int][]int{}
	for i := range messages {
		root := find(i)
		byRoot[root] = append(byRoot[root], i)
	}
	groups := make([][]int, 0, len(byRoot))
	for _, group := range byRoot {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i][0] < groups[j][0] })
	return groups
}
