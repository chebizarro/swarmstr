package channels

import (
	"context"
	"strings"

	"metiq/internal/commitments"
)

const CommitmentEnforcementRewrite = "I can’t commit to future work without opening or claiming a tracked task or flow in this turn."

// CommitmentBacking is the small, transport-safe projection of same-turn tool
// evidence needed by outbound commitment enforcement.
type CommitmentBacking struct {
	SuccessfulCronAdds        int
	SuccessfulTaskFlowActions int
}

type commitmentBackingContextKey struct{}

// ContextWithCommitmentBacking carries tool evidence from the completed agent
// turn to the outbound room transport.
func ContextWithCommitmentBacking(ctx context.Context, backing CommitmentBacking) context.Context {
	return context.WithValue(ctx, commitmentBackingContextKey{}, backing)
}

func commitmentBackingFromContext(ctx context.Context) CommitmentBacking {
	if ctx == nil {
		return CommitmentBacking{}
	}
	backing, _ := ctx.Value(commitmentBackingContextKey{}).(CommitmentBacking)
	return backing
}

// EnforceOutboundCommitment rewrites an unbacked work promise to a
// non-committing explanation. It is opt-in because Metiq has no distinct
// per-room taskflow capability signal; operators enable it only for taskflow
// rooms with config.commitmentEnforcement.
func EnforceOutboundCommitment(ctx context.Context, text string, enabled bool) (string, bool) {
	text = strings.TrimSpace(text)
	if !enabled || !commitments.HasTaskCommitment(text) {
		return text, false
	}
	backing := commitmentBackingFromContext(ctx)
	if backing.SuccessfulTaskFlowActions > 0 {
		return text, false
	}
	if backing.SuccessfulCronAdds > 0 && isReminderCommitment(text) {
		return text, false
	}
	return CommitmentEnforcementRewrite, true
}

func isReminderCommitment(text string) bool {
	lower := strings.ToLower(text)
	for _, phrase := range []string{"remind", "ping", "follow up", "follow-up", "check back", "circle back", "schedule"} {
		if strings.Contains(lower, phrase) {
			return true
		}
	}
	return false
}
