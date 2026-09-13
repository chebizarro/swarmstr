package agent

// BuildPlanningOnlyContinuation creates a new Turn for the retry of a
// planning-only assistant response. It copies base, appends the prior
// planning-only assistant message to History, and joins
// PlanningOnlyRetryInstruction into Context without dropping existing Context.
func BuildPlanningOnlyContinuation(base Turn, priorAssistantText string) Turn {
	out := base

	out.History = append(append([]ConversationMessage{}, base.History...), ConversationMessage{
		Role:    "assistant",
		Content: priorAssistantText,
	})

	if out.Context == "" {
		out.Context = PlanningOnlyRetryInstruction
	} else {
		out.Context += "\n\n" + PlanningOnlyRetryInstruction
	}

	return out
}