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
}

// Get returns the started instance for key.
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

	if err := rt.beginStart(); err != nil {
		return err
	}

	for level, ids := range rt.levelGroups {
		if err := rt.startLevel(ctx, ids); err != nil {
			rt.setState(runtime.StateStartFailed)

			rollbackErr := rt.stopThroughLevel(ctx, level)
			return errors.Join(err, rollbackErr)
		}
	}

	rt.setState(runtime.StateStarted)
	return nil
}

// Stop stops all started components in reverse level order.
func (rt *Runtime) Stop(ctx context.Context) error {
	if rt == nil {
		return fmt.Errorf("runtime cannot be nil")
	}
	if ctx == nil {
		return fmt.Errorf("context cannot be nil")
	}

	if err := rt.beginStop(); err != nil {
		return err
	}

	var errs []error
	for level := len(rt.levelGroups) - 1; level >= 0; level-- {
		if err := rt.stopLevel(ctx, rt.levelGroups[level]); err != nil {
			errs = append(errs, err)
		}
	}

	aggErr := errors.Join(errs...)

	if aggErr != nil {
		rt.setState(runtime.StateStopFailed)
	} else {
		rt.setState(runtime.StateStopped)
	}

	return aggErr
}

func (rt *Runtime) beginStart() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.entries == nil {
		rt.entries = make(map[string]*runtimeEntry)
	}

	switch rt.state {
	case runtime.StateIdle, runtime.StateStopped:
		rt.state = runtime.StateStarting
		return nil
	case runtime.StateStarted:
		return fmt.Errorf("runtime already started: %w", ErrAlreadyStarted)
	default:
		return fmt.Errorf("cannot start runtime from %q: %w", rt.state, ErrInvalidStateTransition)
	}
}

func (rt *Runtime) beginStop() error {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	switch rt.state {
	case runtime.StateStarted, runtime.StateStartFailed:
		rt.state = runtime.StateStopping
		return nil
	default:
		return fmt.Errorf("cannot stop runtime from %q: %w", rt.state, ErrInvalidStateTransition)
	}
}

func (rt *Runtime) setState(next runtime.State) {
	rt.mu.Lock()
	rt.state = next
	rt.mu.Unlock()
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

func (rt *Runtime) stopThroughLevel(ctx context.Context, highestLevel int) error {
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
