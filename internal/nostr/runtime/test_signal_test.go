package runtime

import (
	"context"
	"testing"
	"time"
)

func receiveBeforeTestDeadline[T any](t *testing.T, ch <-chan T, label string) T {
	t.Helper()
	if deadline, ok := t.Deadline(); ok {
		ctx, cancel := context.WithDeadline(context.Background(), deadline.Add(-100*time.Millisecond))
		defer cancel()
		select {
		case v := <-ch:
			return v
		case <-ctx.Done():
			t.Fatalf("deadline waiting for %s", label)
		}
	}
	return <-ch
}

func assertNoReceiveWithin[T any](t *testing.T, ch <-chan T, wait time.Duration, label string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	select {
	case v := <-ch:
		t.Fatalf("unexpected %s: %+v", label, v)
	case <-ctx.Done():
	}
}
