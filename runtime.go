package component

import (
	"context"
	"errors"
	"runtime/debug"
	"sync"
)

type runtimeState uint8

const (
	runtimeIdle runtimeState = iota
	runtimeStarting
	runtimeRunning
	runtimeStopping
	runtimeCleanupPending
	runtimeStopped
)

// Runtime owns one instance of each reachable definition. Copies share state.
// It is one-shot, and its zero value is invalid.
type Runtime struct{ core *runtimeCore }

type runtimeCore struct {
	mu      sync.Mutex
	state   runtimeState
	entries []graphEntry
	indices map[*definition]int
	order   []int
	// Only the active operation writes cells. Value reads them under mu solely
	// while running, when no operation may mutate them.
	values []any
}

// Start constructs nodes serially after their dependencies are ready, then calls Start
// on managed results. It never rolls back. Call Stop even after failed startup.
// Cancellation prevents the next node from starting and waits for the current callback.
func (rt *Runtime) Start(ctx context.Context) error {
	if rt == nil || rt.core == nil {
		return ErrInvalidRuntime
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	core := rt.core
	core.mu.Lock()
	switch core.state {
	case runtimeStarting, runtimeStopping:
		core.mu.Unlock()
		return ErrBusy
	case runtimeIdle:
		core.state = runtimeStarting
	default:
		core.mu.Unlock()
		return ErrAlreadyStarted
	}
	core.mu.Unlock()
	err := core.start(ctx)
	core.mu.Lock()
	switch {
	case err == nil:
		core.state = runtimeRunning
	case core.hasValues():
		core.state = runtimeCleanupPending
	default:
		core.state = runtimeStopped
	}
	core.mu.Unlock()
	return err
}

func (rt *runtimeCore) start(ctx context.Context) error {
	for _, index := range rt.order {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := rt.entries[index]
		arguments := make([]any, len(entry.arguments))
		for position, dependency := range entry.arguments {
			arguments[position] = rt.values[dependency]
		}
		result := invokeNode(ctx, index, entry.definition, arguments, nil)
		// A transferred cell survives even if its Start hook failed or aborted.
		rt.values[index] = result.value
		if result.err != nil {
			return errors.Join(result.err, ctx.Err())
		}
	}
	return ctx.Err()
}

// Stop releases constructed nodes after all their constructed dependents finish.
// A failed Stop remains pending and retains dependencies. The caller must make
// Stop idempotent, including after partial failure. Each call attempts an
// eligible node once; completed stops never repeat. Contexts are passed unchanged.
func (rt *Runtime) Stop(ctx context.Context) error {
	if rt == nil || rt.core == nil {
		return ErrInvalidRuntime
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	core := rt.core
	core.mu.Lock()
	switch core.state {
	case runtimeStarting, runtimeStopping:
		core.mu.Unlock()
		return ErrBusy
	case runtimeIdle, runtimeStopped:
		core.state = runtimeStopped
		core.mu.Unlock()
		return nil
	default:
		core.state = runtimeStopping
	}
	core.mu.Unlock()
	err := core.stop(ctx)
	core.mu.Lock()
	if core.hasValues() {
		core.state = runtimeCleanupPending
	} else {
		core.state = runtimeStopped
	}
	core.mu.Unlock()
	return err
}

func (rt *runtimeCore) stop(ctx context.Context) error {
	failures := make([]error, len(rt.entries))
next:
	for position := len(rt.order) - 1; position >= 0; position-- {
		index := rt.order[position]
		if rt.values[index] == nil {
			continue
		}
		entry := rt.entries[index]
		for _, dependent := range entry.dependents {
			if rt.values[dependent] != nil {
				continue next
			}
		}
		if entry.definition.stop != nil {
			if ctx.Err() != nil {
				continue
			}
			result := invokeNode(ctx, index, entry.definition, nil, rt.values[index])
			if result.err != nil {
				failures[index] = result.err
				continue
			}
		}
		// Pure nodes can complete even after cancellation without invoking user code.
		rt.values[index] = nil
	}
	if rt.hasValues() {
		failures = append(failures, ErrCleanupPending, ctx.Err())
	}
	return errors.Join(failures...)
}

// Value returns a borrowed instance only while the runtime is successfully
// running. The caller must stop using it before calling Stop.
func (rt *Runtime) Value[T any](ref Ref[T]) (T, error) {
	var zero T
	if rt == nil || rt.core == nil {
		return zero, ErrInvalidRuntime
	}
	core := rt.core
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.state != runtimeRunning {
		return zero, ErrUnavailable
	}
	index, exists := core.indices[ref.node]
	if !exists {
		return zero, ErrInvalidReference
	}
	return core.values[index].(*valueCell[T]).value, nil
}

func (rt *runtimeCore) hasValues() bool {
	for _, value := range rt.values {
		if value != nil {
			return true
		}
	}
	return false
}

type invocationResult struct {
	value any
	err   error
}

// Isolation prevents Goexit from terminating the runtime's caller. Deferred
// publication preserves transferred ownership through a Start panic or Goexit.
func invokeNode(ctx context.Context, index int, node *definition, arguments []any, value any) invocationResult {
	results := make(chan invocationResult)
	go func() {
		result := invocationResult{value: value}
		phase := "construct"
		returned := false
		defer func() {
			if recovered := recover(); recovered != nil {
				cause, _ := recovered.(error)
				failure := nodeFailure(index, node, phase, cause)
				failure.kind = ErrPanic
				failure.panicValue = recovered
				failure.Stack = debug.Stack()
				result.err = failure
			} else if !returned {
				failure := nodeFailure(index, node, phase, nil)
				failure.kind = ErrAborted
				result.err = failure
			}
			results <- result
		}()
		if value != nil {
			phase = "stop"
			result.err = node.stop(ctx, value)
		} else {
			result.value, result.err = node.create(ctx, arguments)
			if result.err == nil && node.start != nil {
				phase = "start"
				result.err = node.start(ctx, result.value)
			}
		}
		if result.err != nil {
			result.err = nodeFailure(index, node, phase, result.err)
		}
		returned = true
	}()
	return <-results
}
