package main

import (
	"sync"
	"testing"

	"metiq/internal/gateway/methods"
)

func TestNodeLifecycleFenceRejectsAfterRevocationUntilApproved(t *testing.T) {
	reg := newNodeInvocationRegistry()
	if _, err := withActiveNode(reg, "node-1", func() (map[string]any, error) {
		return applyNodeInvoke(reg, methods.NodeInvokeRequest{NodeID: "node-1", RunID: "run-before", Command: "health"})
	}); err != nil {
		t.Fatalf("active invoke: %v", err)
	}
	if _, err := revokeNode(reg, "node-1", func() (map[string]any, error) {
		reg.RemoveNode("node-1")
		return map[string]any{"ok": true}, nil
	}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := withActiveNode(reg, "node-1", func() (map[string]any, error) {
		return applyNodeInvoke(reg, methods.NodeInvokeRequest{NodeID: "node-1", RunID: "run-after", Command: "health"})
	}); err == nil {
		t.Fatal("revoked node operation unexpectedly succeeded")
	}
	if _, ok := reg.runs["run-before"]; ok {
		t.Fatal("revocation did not clear existing invocation")
	}

	reg.AllowNode("node-1")
	if _, err := withActiveNode(reg, "node-1", func() (map[string]any, error) {
		return applyNodeInvoke(reg, methods.NodeInvokeRequest{NodeID: "node-1", RunID: "run-approved", Command: "health"})
	}); err != nil {
		t.Fatalf("re-approved invoke: %v", err)
	}
}

func TestNodeProgressApplyAndEmitAreSerialized(t *testing.T) {
	reg := newNodeInvocationRegistry()
	reg.Begin(methods.NodeInvokeRequest{NodeID: "node-1", RunID: "run-1", Command: "health"})

	enteredFirst := make(chan struct{})
	secondAttempting := make(chan struct{})
	releaseFirst := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var delivered []int

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, err := applyNodeInvokeProgressAndEmit(reg, methods.NodeInvokeProgressRequest{NodeID: "node-1", InvokeID: "run-1", Seq: 0, Chunk: "zero"}, func(progress nodeInvocationProgressChunk) {
			close(enteredFirst)
			<-releaseFirst
			mu.Lock()
			delivered = append(delivered, progress.Seq)
			mu.Unlock()
		})
		if err != nil {
			t.Errorf("first progress: %v", err)
		}
	}()
	<-enteredFirst
	go func() {
		defer wg.Done()
		close(secondAttempting)
		_, _, err := applyNodeInvokeProgressAndEmit(reg, methods.NodeInvokeProgressRequest{NodeID: "node-1", InvokeID: "run-1", Seq: 1, Chunk: "one"}, func(progress nodeInvocationProgressChunk) {
			mu.Lock()
			delivered = append(delivered, progress.Seq)
			mu.Unlock()
		})
		if err != nil {
			t.Errorf("second progress: %v", err)
		}
	}()
	<-secondAttempting
	close(releaseFirst)
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 2 || delivered[0] != 0 || delivered[1] != 1 {
		t.Fatalf("delivered order=%v", delivered)
	}
}
