package metrics

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mastJudgeFunc func(context.Context, MASTJudgeInput) (MASTJudgeOutput, error)

func (f mastJudgeFunc) Annotate(ctx context.Context, input MASTJudgeInput) (MASTJudgeOutput, error) {
	return f(ctx, input)
}

type fixedMASTResolver struct{ judge MASTJudge }

func (r fixedMASTResolver) ResolveMASTJudge(_, _ string) MASTJudge { return r.judge }

func TestMASTJudgeSchedulerAnnotatesBoundedTranscriptAndScorecard(t *testing.T) {
	scorecard, _, now := newTestScorecard(t, RoomScorecardOptions{})
	var got MASTJudgeInput
	judge := mastJudgeFunc(func(_ context.Context, input MASTJudgeInput) (MASTJudgeOutput, error) {
		got = input
		return MASTJudgeOutput{Labels: []MASTLabel{MASTEchoRestatement, MASTLoopingPair, MASTEchoRestatement}}, nil
	})
	scheduler := &MASTJudgeScheduler{
		Resolver: fixedMASTResolver{judge: judge}, Scorecard: scorecard,
		Now: func() time.Time { return *now },
	}
	events := make([]MASTTranscriptEvent, 6)
	for i := range events {
		events[i] = MASTTranscriptEvent{EventID: string(rune('a' + i)), Author: "agent", CreatedAtMS: int64(i), Content: "turn"}
	}
	policy := MASTJudgePolicy{Enabled: true, Interval: time.Minute, Timeout: time.Second, SampleSize: 3, Provider: "local", Model: "judge"}
	if status := scheduler.Run(context.Background(), "default", "room", policy, events); status != MASTRunAnnotated {
		t.Fatalf("status=%q", status)
	}
	if len(got.Events) != 3 || got.Events[0].EventID != "d" || got.Provider != "local" || got.Model != "judge" {
		t.Fatalf("unbounded or malformed judge input: %+v", got)
	}
	snap := snapshotFor(t, scorecard, "room")
	if snap.MAST.Runs != 1 || snap.MAST.Annotations != 1 || snap.MAST.Errors != 0 ||
		snap.MAST.ByLabel[string(MASTEchoRestatement)] != 1 || snap.MAST.ByLabel[string(MASTLoopingPair)] != 1 {
		t.Fatalf("unexpected MAST snapshot: %+v", snap.MAST)
	}
}

func TestMASTJudgeSchedulerIntervalUnchangedAndNoJudge(t *testing.T) {
	scorecard, _, now := newTestScorecard(t, RoomScorecardOptions{})
	judge := mastJudgeFunc(func(_ context.Context, _ MASTJudgeInput) (MASTJudgeOutput, error) {
		return MASTJudgeOutput{Labels: []MASTLabel{MASTNone}}, nil
	})
	scheduler := &MASTJudgeScheduler{Resolver: fixedMASTResolver{judge: judge}, Scorecard: scorecard, Now: func() time.Time { return *now }}
	policy := MASTJudgePolicy{Enabled: true, Interval: time.Minute, Timeout: time.Second}
	events := []MASTTranscriptEvent{{EventID: "one", Author: "agent", Content: "ok"}}
	if got := scheduler.Run(context.Background(), "a", "room", policy, events); got != MASTRunAnnotated {
		t.Fatalf("first run=%q", got)
	}
	if got := scheduler.Run(context.Background(), "a", "room", policy, events); got != MASTRunInterval {
		t.Fatalf("interval run=%q", got)
	}
	*now = now.Add(2 * time.Minute)
	if got := scheduler.Run(context.Background(), "a", "room", policy, events); got != MASTRunUnchanged {
		t.Fatalf("unchanged run=%q", got)
	}
	noJudge := &MASTJudgeScheduler{Scorecard: scorecard, Now: func() time.Time { return *now }}
	if got := noJudge.Run(context.Background(), "b", "room", policy, events); got != MASTRunNoJudge {
		t.Fatalf("no-judge run=%q", got)
	}
}

func TestMASTJudgeSchedulerFailsQuietOnJudgeOrLabelError(t *testing.T) {
	for name, judge := range map[string]MASTJudge{
		"judge": mastJudgeFunc(func(_ context.Context, _ MASTJudgeInput) (MASTJudgeOutput, error) {
			return MASTJudgeOutput{}, errors.New("provider down")
		}),
		"labels": mastJudgeFunc(func(_ context.Context, _ MASTJudgeInput) (MASTJudgeOutput, error) {
			return MASTJudgeOutput{Labels: []MASTLabel{MASTNone, MASTOther}}, nil
		}),
	} {
		t.Run(name, func(t *testing.T) {
			scorecard, _, _ := newTestScorecard(t, RoomScorecardOptions{})
			scheduler := &MASTJudgeScheduler{Resolver: fixedMASTResolver{judge: judge}, Scorecard: scorecard}
			status := scheduler.Run(context.Background(), "a", name, MASTJudgePolicy{Enabled: true}, []MASTTranscriptEvent{{EventID: "one"}})
			if status != MASTRunError {
				t.Fatalf("status=%q", status)
			}
			snap := snapshotFor(t, scorecard, name)
			if snap.MAST.Runs != 1 || snap.MAST.Errors != 1 || snap.MAST.Annotations != 0 {
				t.Fatalf("unexpected failure snapshot: %+v", snap.MAST)
			}
		})
	}
}
