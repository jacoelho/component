package component

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jacoelho/component/internal/runtime"
)

type runtimeEntry struct {
	constructor func(*Runtime) (any, error)
	instance    any
}

// Runtime executes lifecycle operations for a compiled plan.
type Runtime struct {
	mu          sync.Mutex
	entries     map[string]*runtimeEntry
	levelGroups [][]string
	state       runtime.State
	fsm         *runtime.LifecycleFSM
}

// Get returns the available instance for key.
// Retrieval is allowed only while runtime startup is in progress or completed.
func Get[T Lifecycle](rt *Runtime, key Key[T]) (T, error) {
	var zero T

	if rt == nil {
		return zero, fmt.Errorf("runtime cannot be nil")
	}

	id := key.id()
	if id == "" {
		return zero, fmt.Errorf("component key cannot be empty")
	}

	rt.mu.Lock()
	defer rt.mu.Unlock()

	switch rt.state {
	case runtime.StateStarting, runtime.StateStarted:
	default:
		return zero, wrapComponentError(id, fmt.Sprintf("not available in state %q", rt.state), ErrNotStarted)
	}

	ent, ok := rt.entries[id]
	if !ok {
		return zero, wrapRetrievalError(id, ErrNotRegistered)
	}
	if ent.instance == nil {
		return zero, wrapComponentError(id, "not started", ErrNotStarted)
	}

	inst, ok := ent.instance.(T)
	if !ok {
		actualType := "nil"
		if ent.instance != nil {
			actualType = fmt.Sprintf("%T", ent.instance)
		}
		var expectedType T
		expectedTypeStr := fmt.Sprintf("%T", expectedType)
		return zero, fmt.Errorf("component %q type assertion failed: expected %s, got %s: %w",
			id, expectedTypeStr, actualType, ErrIncorrectType)
	}
	return inst, nil
}

// Start starts all components level by level.
func (rt *Runtime) Start(ctx context.Context) error {
	if rt == nil {
		return fmt.Errorf("runtime cannot be nil")
	}
	if ctx == nil {
		return fmt.Errorf("context cannot be nil")
	}

	if err := rt.beginStartTransaction(); err != nil {
		return err
	}

	for level, ids := range rt.levelGroups {
		if err := rt.startLevel(ctx, ids); err != nil {
			transitionErr := rt.completeTransition(runtime.EventStartFailed)
			rollbackErr := rt.stopLevels(ctx, level)
			rollbackEvent := runtime.EventStartRollbackFailed
			if rollbackErr == nil {
				rollbackEvent = runtime.EventStartRollbackSucceeded
			}

			rollbackTransitionErr := rt.completeTransition(rollbackEvent)
			return errors.Join(err, transitionErr, rollbackErr, rollbackTransitionErr)
		}
	}

	return rt.completeTransition(runtime.EventStartSucceeded)
}

// Stop stops all started components in reverse level order.
func (rt *Runtime) Stop(ctx context.Context) error {
	if rt == nil {
		return fmt.Errorf("runtime cannot be nil")
	}
	if ctx == nil {
		return fmt.Errorf("context cannot be nil")
	}

	if err := rt.beginStopTransaction(); err != nil {
		return err
	}

	aggErr := rt.stopLevels(ctx, len(rt.levelGroups)-1)

	if aggErr != nil {
		transitionErr := rt.completeTransition(runtime.EventStopFailed)
		return errors.Join(aggErr, transitionErr)
	}

	return rt.completeTransition(runtime.EventStopSucceeded)
}

func (rt *Runtime) beginStartTransaction() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.entries == nil {
		rt.entries = make(map[string]*runtimeEntry)
	}

	return rt.transitionLockedExpectingAction(
		runtime.EventStartRequested,
		runtime.ActionRunStart,
		"start request",
	)
}

func (rt *Runtime) beginStopTransaction() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	return rt.transitionLockedExpectingAction(
		runtime.EventStopRequested,
		runtime.ActionRunStop,
		"stop request",
	)
}

func (rt *Runtime) completeTransition(event runtime.Event) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	return rt.transitionLockedExpectingAction(
		event,
		runtime.ActionNone,
		fmt.Sprintf("completion event %s", event),
	)
}

func (rt *Runtime) transitionLockedExpectingAction(
	event runtime.Event,
	expected runtime.Action,
	context string,
) error {
	action, err := rt.transitionLocked(event)
	if err != nil {
		return err
	}
	if action != expected {
		return fmt.Errorf("unexpected lifecycle action for %s: %s", context, action)
	}

	return nil
}

func (rt *Runtime) transitionLocked(event runtime.Event) (runtime.Action, error) {
	if rt.fsm == nil {
		rt.fsm = runtime.NewLifecycleFSM()
	}

	current := rt.state
	next, action, err := rt.fsm.Transition(current, event)
	if err != nil {
		return runtime.ActionNone, mapTransitionError(current, event, err)
	}

	rt.state = next
	return action, nil
}

func mapTransitionError(state runtime.State, event runtime.Event, err error) error {
	switch {
	case errors.Is(err, runtime.ErrAlreadyStartedTransition):
		return fmt.Errorf("runtime already started: %w", ErrAlreadyStarted)
	case errors.Is(err, runtime.ErrInvalidTransition):
		switch event {
		case runtime.EventStartRequested:
			return fmt.Errorf("cannot start runtime from %q: %w", state, ErrInvalidStateTransition)
		case runtime.EventStopRequested:
			return fmt.Errorf("cannot stop runtime from %q: %w", state, ErrInvalidStateTransition)
		default:
			return fmt.Errorf("invalid runtime transition from %q on %s: %w", state, event, err)
		}
	default:
		return err
	}
}

func (rt *Runtime) startLevel(ctx context.Context, ids []string) error {
	ec := runtime.NewErrorCollector()

	type entryData struct {
		id          string
		constructor func(*Runtime) (any, error)
	}

	rt.mu.Lock()
	entries := make([]entryData, 0, len(ids))
	for _, id := range ids {
		if ent, exists := rt.entries[id]; exists {
			entries = append(entries, entryData{id: id, constructor: ent.constructor})
		} else {
			ec.Addf("missing entry for type %q: %w", id, ErrNotRegistered)
		}
	}
	rt.mu.Unlock()

	var wg sync.WaitGroup
	for _, entry := range entries {
		wg.Add(1)
		go func(entry entryData) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					ec.Addf("panic during start for %q %v: %w", entry.id, r, ErrPanic)
				}
			}()

			instance, err := entry.constructor(rt)
			if err != nil {
				ec.Addf("provide for %q: %w", entry.id, err)
				return
			}

			lc, ok := instance.(Lifecycle)
			if !ok {
				ec.Addf("%T does not implement Lifecycle", instance)
				return
			}

			if err := lc.Start(ctx); err != nil {
				ec.Addf("start failed for %q: %w", entry.id, err)
				return
			}

			rt.mu.Lock()
			if ent, exists := rt.entries[entry.id]; exists {
				if ent.instance != nil {
					ec.Addf("duplicate initialization of %q: %w", entry.id, ErrAlreadyInitialized)
					rt.mu.Unlock()
					return
				}
				ent.instance = instance
			}
			rt.mu.Unlock()
		}(entry)
	}

	wg.Wait()
	return ec.Err()
}

func (rt *Runtime) stopLevels(ctx context.Context, highestLevel int) error {
	if highestLevel >= len(rt.levelGroups) {
		highestLevel = len(rt.levelGroups) - 1
	}
	if highestLevel < 0 {
		return nil
	}

	var errs []error
	for level := highestLevel; level >= 0; level-- {
		if err := rt.stopLevel(ctx, rt.levelGroups[level]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (rt *Runtime) stopLevel(ctx context.Context, ids []string) error {
	ec := runtime.NewErrorCollector()

	type instanceData struct {
		id        string
		lifecycle Lifecycle
	}

	rt.mu.Lock()
	instances := make([]instanceData, 0, len(ids))
	for _, id := range ids {
		ent, exists := rt.entries[id]
		if !exists {
			ec.Addf("missing entry for type %q: %w", id, ErrNotRegistered)
			continue
		}
		if ent.instance == nil {
			continue
		}
		lc, ok := ent.instance.(Lifecycle)
		if !ok {
			ec.Addf("%T does not implement Lifecycle", ent.instance)
			continue
		}
		instances = append(instances, instanceData{id: id, lifecycle: lc})
	}
	rt.mu.Unlock()

	var wg sync.WaitGroup
	for _, inst := range instances {
		wg.Add(1)
		go func(inst instanceData) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					ec.Addf("panic during stop for %q %v: %w", inst.id, r, ErrPanic)
				}
			}()

			if err := inst.lifecycle.Stop(ctx); err != nil {
				ec.Addf("stop failed for %q: %w", inst.id, err)
				return
			}

			rt.mu.Lock()
			if ent, exists := rt.entries[inst.id]; exists {
				ent.instance = nil
			}
			rt.mu.Unlock()
		}(inst)
	}

	wg.Wait()
	return ec.Err()
}
