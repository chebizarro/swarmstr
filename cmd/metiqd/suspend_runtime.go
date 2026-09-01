package main

import (
	"context"
	"log"

	"metiq/internal/agent"
	suspendpkg "metiq/internal/gateway/suspend"
)

// suspendAdmissionRuntime places every model turn behind the coordinator's
// atomic interactive-work lease. It preserves the streaming surface so wrapping
// a provider runtime does not downgrade chat streaming.
type suspendAdmissionContextKey struct{}

func contextWithSuspendAdmission(ctx context.Context) context.Context {
	return context.WithValue(contextWithoutNil(ctx), suspendAdmissionContextKey{}, true)
}

func hasSuspendAdmission(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	admitted, _ := ctx.Value(suspendAdmissionContextKey{}).(bool)
	return admitted
}

type suspendAdmissionRuntime struct {
	base        agent.Runtime
	coordinator *suspendpkg.Coordinator
}

func wrapRuntimeForSuspend(rt agent.Runtime, coordinator *suspendpkg.Coordinator) agent.Runtime {
	if rt == nil || coordinator == nil {
		return rt
	}
	if wrapped, ok := rt.(*suspendAdmissionRuntime); ok && wrapped.coordinator == coordinator {
		return rt
	}
	return &suspendAdmissionRuntime{base: rt, coordinator: coordinator}
}

func (r *suspendAdmissionRuntime) withLease(ctx context.Context, run func(context.Context) (agent.TurnResult, error)) (agent.TurnResult, error) {
	if hasSuspendAdmission(ctx) {
		return run(ctx)
	}
	lease, err := r.coordinator.BeginWork()
	if err != nil {
		return agent.TurnResult{}, err
	}
	defer func() {
		if err := lease.Release(); err != nil {
			log.Printf("suspend admission lease release failed: %v", err)
		}
	}()
	return run(contextWithSuspendAdmission(ctx))
}

func (r *suspendAdmissionRuntime) ProcessTurn(ctx context.Context, turn agent.Turn) (agent.TurnResult, error) {
	return r.withLease(ctx, func(admitted context.Context) (agent.TurnResult, error) {
		return r.base.ProcessTurn(admitted, turn)
	})
}

func (r *suspendAdmissionRuntime) ProcessTurnStreaming(ctx context.Context, turn agent.Turn, onChunk func(string)) (agent.TurnResult, error) {
	return r.withLease(ctx, func(admitted context.Context) (agent.TurnResult, error) {
		if streaming, ok := r.base.(agent.StreamingRuntime); ok {
			return streaming.ProcessTurnStreaming(admitted, turn, onChunk)
		}
		return r.base.ProcessTurn(admitted, turn)
	})
}
