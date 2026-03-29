package autoreply_test

import (
	"context"
	"testing"
	"time"

	"metiq/internal/autoreply"
)

func TestParse_returnsNilForNonSlash(t *testing.T) {
	cases := []string{"hello", "", "   ", "help", "status"}
	for _, c := range cases {
		if cmd := autoreply.Parse(c); cmd != nil {
			t.Errorf("Parse(%q) = %v, want nil", c, cmd)
		}
	}
}

func TestParse_extractsNameAndArgs(t *testing.T) {
	cmd := autoreply.Parse("/model gpt-4o turbo")
	if cmd == nil {
		t.Fatal("Parse returned nil for slash command")
	}
	if cmd.Name != "model" {
		t.Errorf("Name = %q, want %q", cmd.Name, "model")
	}
	if len(cmd.Args) != 2 || cmd.Args[0] != "gpt-4o" || cmd.Args[1] != "turbo" {
		t.Errorf("Args = %v, want [gpt-4o turbo]", cmd.Args)
	}
}

func TestParse_normalisesCaseAndTrimSpace(t *testing.T) {
	cmd := autoreply.Parse("  /HELP  ")
	if cmd == nil || cmd.Name != "help" {
		t.Errorf("expected cmd.Name=help, got %v", cmd)
	}
}

func TestRouter_dispatch(t *testing.T) {
	r := autoreply.NewRouter()
	called := false
	r.Register("ping", func(_ context.Context, cmd autoreply.Command) (string, error) {
		called = true
		return "pong", nil
	})

	cmd := autoreply.Parse("/ping")
	reply, handled, err := r.Dispatch(context.Background(), cmd)
	if !handled {
		t.Fatal("expected handled=true")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "pong" {
		t.Errorf("reply = %q, want %q", reply, "pong")
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestRouter_unknownCommandNotHandled(t *testing.T) {
	r := autoreply.NewRouter()
	cmd := autoreply.Parse("/unknown")
	_, handled, err := r.Dispatch(context.Background(), cmd)
	if handled {
		t.Error("expected handled=false for unknown command")
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRouter_registered(t *testing.T) {
	r := autoreply.NewRouter()
	r.Register("zebra", func(_ context.Context, _ autoreply.Command) (string, error) { return "", nil })
	r.Register("apple", func(_ context.Context, _ autoreply.Command) (string, error) { return "", nil })

	names := r.Registered()
	if len(names) != 2 || names[0] != "apple" || names[1] != "zebra" {
		t.Errorf("Registered() = %v, want [apple zebra]", names)
	}
}

func TestSessionTurns_acquire(t *testing.T) {
	st := autoreply.NewSessionTurns()

	release, ok := st.TryAcquire("s1")
	if !ok {
		t.Fatal("first TryAcquire should succeed")
	}

	// While s1 is held, a second attempt should fail.
	_, ok2 := st.TryAcquire("s1")
	if ok2 {
		t.Error("second TryAcquire should fail while slot is held")
	}

	// Different session should succeed independently.
	release2, ok3 := st.TryAcquire("s2")
	if !ok3 {
		t.Error("TryAcquire for different session should succeed")
	}
	release2()

	// After release, re-acquisition of s1 should succeed.
	release()
	release3, ok4 := st.TryAcquire("s1")
	if !ok4 {
		t.Error("TryAcquire should succeed after release")
	}
	release3()
}

func TestSessionTurns_AcquireWaitsAndCancels(t *testing.T) {
	st := autoreply.NewSessionTurns()
	release, ok := st.TryAcquire("s1")
	if !ok {
		t.Fatal("initial TryAcquire should succeed")
	}

	acquired := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		release2, err := st.Acquire(ctx, "s1")
		if err != nil {
			t.Errorf("Acquire wait failed: %v", err)
			close(acquired)
			return
		}
		release2()
		close(acquired)
	}()

	time.Sleep(60 * time.Millisecond)
	release()
	<-acquired

	release3, ok := st.TryAcquire("s1")
	if !ok {
		t.Fatal("slot should be available after waiting acquire completes")
	}
	release3()

	release4, ok := st.TryAcquire("s2")
	if !ok {
		t.Fatal("TryAcquire for cancellation test should succeed")
	}
	defer release4()

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := st.Acquire(ctx, "s2"); err == nil {
		t.Fatal("expected Acquire to fail on context timeout")
	}
}
