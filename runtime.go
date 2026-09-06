package component

import (
	"container/heap"
	"context"
	"errors"
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
	mu          sync.Mutex
	state       runtimeState
	entries     []graphEntry
	indices     map[*definition]int
	parallelism int
	// Only the active operation writes cells. Value reads them under mu solely
	// while running, when no operation may mutate them.
	values []any
}

// Start constructs nodes after their dependencies are ready, then calls Start
// on managed results. It never rolls back. Call Stop even after failed startup.
// Cancellation stops new dispatch and waits for all dispatched callbacks.
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
	remaining := make([]int, len(rt.entries))
	ready := &readyQueue{}
	for index, entry := range rt.entries {
		remaining[index] = len(entry.dependencies)
		if remaining[index] == 0 {
			heap.Push(ready, index)
		}
	}
	results := make(chan invocationResult, rt.parallelism)
	failures := make([]error, len(rt.entries))
	inFlight := 0
	failed := false
	for {
		for !failed && ready.Len() != 0 && inFlight < rt.parallelism && ctx.Err() == nil {
			index := heap.Pop(ready).(int)
			entry := rt.entries[index]
			arguments := make([]any, len(entry.arguments))
			for position, dependency := range entry.arguments {
				arguments[position] = rt.values[dependency]
			}
			inFlight++
			go invokeNode(ctx, index, entry.definition, arguments, nil, results)
		}
		if inFlight == 0 {
			break
		}
		result := <-results
		inFlight--
		// A transferred cell survives even if its Start hook failed or aborted.
		rt.values[result.index] = result.value
		if result.err != nil {
			failures[result.index] = result.err
			failed = true
			continue
		}
		for _, dependent := range rt.entries[result.index].dependents {
			remaining[dependent]--
			if remaining[dependent] == 0 {
				heap.Push(ready, dependent)
			}
		}
	}
	return operationErrors(failures, ctx.Err(), false)
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
	remaining := make([]int, len(rt.entries))
	ready := &readyQueue{}
	for index, entry := range rt.entries {
		if rt.values[index] == nil {
			continue
		}
		for _, dependent := range entry.dependents {
			if rt.values[dependent] != nil {
				remaining[index]++
			}
		}
		if remaining[index] == 0 {
			heap.Push(ready, index)
		}
	}
	results := make(chan invocationResult, rt.parallelism)
	failures := make([]error, len(rt.entries))
	inFlight := 0
	complete := func(index int) {
		rt.values[index] = nil
		for _, dependency := range rt.entries[index].dependencies {
			remaining[dependency]--
			if remaining[dependency] == 0 {
				heap.Push(ready, dependency)
			}
		}
	}
	for {
		for ready.Len() != 0 {
			index := (*ready)[0]
			node := rt.entries[index].definition
			if node.stop == nil {
				heap.Pop(ready)
				complete(index)
				continue
			}
			if ctx.Err() != nil {
				// The retained cell makes this node eligible again on the next
				// Stop. Continue draining pure nodes without invoking callbacks.
				heap.Pop(ready)
				continue
			}
			if inFlight == rt.parallelism {
				break
			}
			heap.Pop(ready)
			inFlight++
			go invokeNode(ctx, index, node, nil, rt.values[index], results)
		}
		if inFlight == 0 {
			break
		}
		result := <-results
		inFlight--
		if result.err != nil {
			failures[result.index] = result.err
			continue
		}
		complete(result.index)
	}
	pending := rt.hasValues()
	var contextErr error
	if pending {
		contextErr = ctx.Err()
	}
	return operationErrors(failures, contextErr, pending)
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

func operationErrors(failures []error, contextErr error, pending bool) error {
	var errs []error
	for _, failure := range failures {
		if failure != nil {
			errs = append(errs, failure)
		}
	}
	if pending {
		errs = append(errs, ErrCleanupPending)
	}
	if contextErr != nil {
		errs = append(errs, contextErr)
	}
	// Joining does not format, unwrap, or classify user errors.
	return errors.Join(errs...)
}

type readyQueue []int

func (q readyQueue) Len() int           { return len(q) }
func (q readyQueue) Less(i, j int) bool { return q[i] < q[j] }
func (q readyQueue) Swap(i, j int)      { q[i], q[j] = q[j], q[i] }
func (q *readyQueue) Push(value any)    { *q = append(*q, value.(int)) }
func (q *readyQueue) Pop() any {
	last := len(*q) - 1
	value := (*q)[last]
	*q = (*q)[:last]
	return value
}
