package component

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
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

// Runtime owns a compiled lifecycle graph. Copies share the same runtime
// identity. It is one-shot: after any Start attempt or any Stop call it cannot
// be started again. A zero Runtime is invalid.
type Runtime struct {
	core *runtimeCore
}

type runtimeCore struct {
	entries   []runtimeEntry
	frontiers [][]int
	cleanup   []bool
	mu        sync.Mutex
	state     runtimeState
}

type runtimeEntry struct {
	graphEntry
	lifecycle Lifecycle
}

type lifecyclePhase string

const (
	phaseConfigure lifecyclePhase = "configure"
	phaseStart     lifecyclePhase = "start"
	phaseStop      lifecyclePhase = "stop"
)

type indexedError struct {
	err   error
	index int
}

// Start configures the complete graph and then starts it. Dependency
// frontiers run in order; nodes within a frontier run concurrently. Start does
// not call Stop after a failure; the caller decides whether and how to clean up
// nodes whose Configure callback was invoked.
func (rt *Runtime) Start(ctx context.Context) error {
	if rt == nil || rt.core == nil {
		return errors.New("component: start on invalid runtime")
	}
	return rt.core.start(ctx)
}

func (rt *runtimeCore) start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("component: Start called with nil context")
	}

	rt.mu.Lock()
	switch rt.state {
	case runtimeIdle:
		rt.state = runtimeStarting
	case runtimeCleanupPending:
		rt.mu.Unlock()
		return ErrCleanupPending
	case runtimeStarting, runtimeStopping:
		rt.mu.Unlock()
		return ErrRuntimeBusy
	case runtimeRunning, runtimeStopped:
		rt.mu.Unlock()
		return ErrAlreadyStarted
	default:
		rt.mu.Unlock()
		return errors.New("component: invalid runtime state")
	}
	rt.mu.Unlock()

	if err := rt.runConfigure(ctx); err != nil {
		return rt.failStart(err)
	}
	if err := rt.runStart(ctx); err != nil {
		return rt.failStart(err)
	}

	rt.mu.Lock()
	rt.state = runtimeRunning
	rt.mu.Unlock()
	return nil
}

func (rt *runtimeCore) runConfigure(ctx context.Context) error {
	for _, frontier := range rt.frontiers {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("component: configure cancelled: %w", err)
		}
		rt.mu.Lock()
		for _, index := range frontier {
			rt.cleanup[index] = true
		}
		rt.mu.Unlock()

		if err := rt.invokeFrontier(ctx, frontier, phaseConfigure); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("component: configure cancelled: %w", err)
	}
	return nil
}

func (rt *runtimeCore) runStart(ctx context.Context) error {
	for _, frontier := range rt.frontiers {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("component: start cancelled: %w", err)
		}
		if err := rt.invokeFrontier(ctx, frontier, phaseStart); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("component: start cancelled: %w", err)
	}
	return nil
}

func (rt *runtimeCore) failStart(startErr error) error {
	rt.mu.Lock()
	if rt.hasCleanupLocked() {
		rt.state = runtimeCleanupPending
	} else {
		rt.state = runtimeStopped
	}
	rt.mu.Unlock()
	return startErr
}

// Stop releases every configured or started node in reverse dependency order.
// It is idempotent after complete cleanup and retryable after incomplete
// cleanup. Calling Stop before Start returns nil without invoking callbacks and
// consumes the one-shot runtime. Stop passes the caller's context directly to
// lifecycle callbacks.
func (rt *Runtime) Stop(ctx context.Context) error {
	if rt == nil || rt.core == nil {
		return errors.New("component: stop on invalid runtime")
	}
	return rt.core.stop(ctx)
}

func (rt *runtimeCore) stop(ctx context.Context) error {
	if ctx == nil {
		return errors.New("component: Stop called with nil context")
	}

	rt.mu.Lock()
	switch rt.state {
	case runtimeIdle:
		rt.state = runtimeStopped
		rt.mu.Unlock()
		return nil
	case runtimeStopped:
		rt.mu.Unlock()
		return nil
	case runtimeRunning, runtimeCleanupPending:
		rt.state = runtimeStopping
	case runtimeStarting, runtimeStopping:
		rt.mu.Unlock()
		return ErrRuntimeBusy
	default:
		rt.mu.Unlock()
		return errors.New("component: invalid runtime state")
	}
	rt.mu.Unlock()

	err := rt.stopRemaining(ctx)
	rt.mu.Lock()
	if rt.hasCleanupLocked() {
		rt.state = runtimeCleanupPending
	} else {
		rt.state = runtimeStopped
	}
	rt.mu.Unlock()
	return err
}

func (rt *runtimeCore) stopRemaining(ctx context.Context) error {
	rt.mu.Lock()
	active := append([]bool(nil), rt.cleanup...)
	rt.mu.Unlock()

	activeCount := 0
	activeDependents := make([]int, len(rt.entries))
	for index, requiresCleanup := range active {
		if !requiresCleanup {
			continue
		}
		activeCount++
		for _, dependent := range rt.entries[index].dependents {
			if active[dependent] {
				activeDependents[index]++
			}
		}
	}
	if activeCount == 0 {
		return nil
	}

	results := make(chan indexedError, activeCount)
	launched := make([]bool, len(rt.entries))
	inFlight := 0
	launch := func(index int) {
		launched[index] = true
		inFlight++
		go invokeLifecycle(
			ctx,
			index,
			rt.entries[index],
			phaseStop,
			results,
		)
	}
	for index, requiresCleanup := range active {
		if requiresCleanup && activeDependents[index] == 0 {
			launch(index)
		}
	}
	if inFlight == 0 {
		return errors.New("component: cleanup graph has no eligible node")
	}

	failures := make([]error, len(rt.entries))
	for inFlight != 0 {
		result := <-results
		inFlight--
		if result.err != nil {
			failures[result.index] = result.err
			continue
		}

		active[result.index] = false
		rt.mu.Lock()
		rt.cleanup[result.index] = false
		rt.mu.Unlock()
		for _, dependency := range rt.entries[result.index].dependencies {
			if !active[dependency] {
				continue
			}
			activeDependents[dependency]--
			if !launched[dependency] &&
				activeDependents[dependency] == 0 {
				launch(dependency)
			}
		}
	}

	errs := make([]error, 0, activeCount)
	for _, failure := range failures {
		if failure != nil {
			errs = append(errs, failure)
		}
	}
	return joinErrors(errs)
}

func (rt *runtimeCore) hasCleanupLocked() bool {
	for _, active := range rt.cleanup {
		if active {
			return true
		}
	}
	return false
}

func (rt *runtimeCore) invokeFrontier(
	ctx context.Context,
	frontier []int,
	phase lifecyclePhase,
) error {
	results := rt.invokeFrontierErrors(ctx, frontier, phase)
	errs := make([]error, 0, len(results))
	for _, result := range results {
		if result.err != nil {
			errs = append(errs, result.err)
		}
	}
	return joinErrors(errs)
}

func (rt *runtimeCore) invokeFrontierErrors(
	ctx context.Context,
	frontier []int,
	phase lifecyclePhase,
) []indexedError {
	resultChannel := make(chan indexedError, len(frontier))
	for _, index := range frontier {
		entry := rt.entries[index]
		go invokeLifecycle(ctx, index, entry, phase, resultChannel)
	}

	results := make([]indexedError, 0, len(frontier))
	for range frontier {
		results = append(results, <-resultChannel)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].index < results[j].index
	})
	return results
}

func invokeLifecycle(
	ctx context.Context,
	index int,
	entry runtimeEntry,
	phase lifecyclePhase,
	results chan<- indexedError,
) {
	returned := false
	defer func() {
		if recovered := recover(); recovered != nil {
			results <- indexedError{
				index: index,
				err:   lifecyclePanicError(entry.node, phase, recovered),
			}
			return
		}
		if !returned {
			results <- indexedError{
				index: index,
				err: fmt.Errorf(
					"%w: node %q phase %s",
					ErrLifecycleAborted,
					entry.node.label,
					phase,
				),
			}
		}
	}()

	var err error
	switch phase {
	case phaseConfigure:
		err = entry.lifecycle.Configure(ctx)
	case phaseStart:
		err = entry.lifecycle.Start(ctx)
	case phaseStop:
		err = entry.lifecycle.Stop(ctx)
	default:
		err = errors.New("component: unknown lifecycle phase")
	}
	returned = true
	if err != nil {
		err = fmt.Errorf(
			"component: node %q phase %s: %w",
			entry.node.label,
			phase,
			err,
		)
	}
	results <- indexedError{index: index, err: err}
}

func lifecyclePanicError(
	node nodeDescriptor,
	phase lifecyclePhase,
	recovered any,
) error {
	stack := debug.Stack()
	if cause, ok := recovered.(error); ok {
		return fmt.Errorf(
			"%w: node %q phase %s: %w\n%s",
			ErrPanic,
			node.label,
			phase,
			cause,
			stack,
		)
	}
	return fmt.Errorf(
		"%w: node %q phase %s: %v\n%s",
		ErrPanic,
		node.label,
		phase,
		recovered,
		stack,
	)
}

func joinErrors(errs []error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	default:
		return errors.Join(errs...)
	}
}
