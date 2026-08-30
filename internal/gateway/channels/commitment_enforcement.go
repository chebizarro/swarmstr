package channels

import (
	"context"
	"strings"
	"sync"

	"metiq/internal/commitments"
)

const CommitmentEnforcementRewrite = "I can’t commit to future work without opening or claiming a tracked task or flow in this turn."

// CommitmentBacking is the transport-safe projection of same-turn tool
// evidence needed by outbound commitment enforcement. Counts are retained for
// reminder compatibility and diagnostics; task/flow promises require concrete
// References resolved against live state.
type CommitmentBacking struct {
	SuccessfulCronAdds        int
	SuccessfulTaskFlowActions int
	RoomKey                   string
	TurnID                    string
	References                []string
}

// CommitmentBackingRequest asks the daemon deployment to validate concrete
// task:/flow: handles and durably correlate the promise to their lifecycle.
type CommitmentBackingRequest struct {
	RoomKey    string
	TurnID     string
	Text       string
	References []string
}

// CommitmentBackingResolution contains only handles that currently exist,
// belong to the room where applicable, and are not terminal.
type CommitmentBackingResolution struct {
	LiveReferences []string
}

// CommitmentBackingResolver resolves handles against deployment-owned live
// task/flow state. Errors fail quiet: the promise is rewritten rather than
// letting unverifiable work escape.
type CommitmentBackingResolver func(context.Context, CommitmentBackingRequest) (CommitmentBackingResolution, error)

var commitmentBackingResolverRegistry struct {
	sync.RWMutex
	resolver   CommitmentBackingResolver
	generation uint64
}

// RegisterCommitmentBackingResolver installs the deployment resolver and
// returns an unregister function that cannot remove a later registration.
func RegisterCommitmentBackingResolver(resolver CommitmentBackingResolver) func() {
	commitmentBackingResolverRegistry.Lock()
	commitmentBackingResolverRegistry.generation++
	generation := commitmentBackingResolverRegistry.generation
	commitmentBackingResolverRegistry.resolver = resolver
	commitmentBackingResolverRegistry.Unlock()
	return func() {
		commitmentBackingResolverRegistry.Lock()
		if commitmentBackingResolverRegistry.generation == generation {
			commitmentBackingResolverRegistry.resolver = nil
		}
		commitmentBackingResolverRegistry.Unlock()
	}
}

func currentCommitmentBackingResolver() CommitmentBackingResolver {
	commitmentBackingResolverRegistry.RLock()
	defer commitmentBackingResolverRegistry.RUnlock()
	return commitmentBackingResolverRegistry.resolver
}

type commitmentBackingContextKey struct{}

// ContextWithCommitmentBacking carries structured tool evidence from the
// completed agent turn to the outbound room transport.
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
// non-committing explanation. Reminder promises may be backed by a successful
// scheduler action; task/flow promises require at least one live resolved
// handle. Fabricated handles, terminal records, resolver failures, and legacy
// success counts do not pass the guard.
func EnforceOutboundCommitment(ctx context.Context, text string, enabled bool) (string, bool) {
	text = strings.TrimSpace(text)
	if !enabled || !commitments.HasTaskCommitment(text) {
		return text, false
	}
	backing := commitmentBackingFromContext(ctx)
	if backing.SuccessfulCronAdds > 0 && isReminderCommitment(text) {
		return text, false
	}
	resolver := currentCommitmentBackingResolver()
	if resolver == nil || len(backing.References) == 0 {
		return CommitmentEnforcementRewrite, true
	}
	resolution, err := resolver(ctx, CommitmentBackingRequest{
		RoomKey: backing.RoomKey, TurnID: backing.TurnID, Text: text,
		References: append([]string(nil), backing.References...),
	})
	if err != nil || len(resolution.LiveReferences) == 0 {
		return CommitmentEnforcementRewrite, true
	}
	return text, false
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
