package component

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/jacoelho/component/internal/graph"
)

// Registry stores component declarations.
type Registry struct {
	mu      sync.Mutex
	entries map[string]*componentSpec
}

// NewRegistry creates an empty component registry.
func NewRegistry() *Registry {
	return &Registry{
		entries: make(map[string]*componentSpec),
	}
}

// Provide registers a component declaration.
// Dependencies are validated for cycles immediately, while missing dependencies
// are validated at Compile time.
func Provide[T Lifecycle](
	r *Registry,
	key Key[T],
	fn Constructor[T],
	deps ...Keyer,
) error {
	if r == nil {
		return fmt.Errorf("registry cannot be nil")
	}
	if fn == nil {
		return fmt.Errorf("constructor function cannot be nil")
	}

	id := key.id()
	if id == "" {
		return fmt.Errorf("component key cannot be empty")
	}

	dependencies := make([]string, 0, len(deps))
	for _, d := range deps {
		if d == nil {
			return fmt.Errorf("dependency cannot be nil")
		}
		depID := d.id()
		if depID == "" {
			return fmt.Errorf("dependency ID cannot be empty")
		}
		dependencies = append(dependencies, depID)
	}

	candidateEntry := &componentSpec{
		constructor: func(rt *Runtime) (any, error) {
			return fn(rt)
		},
		dependencies: dependencies,
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.entries == nil {
		r.entries = make(map[string]*componentSpec)
	}

	if _, exists := r.entries[id]; exists {
		return wrapRegistrationError(id, ErrAlreadyRegistered)
	}

	candidate := cloneSpecs(r.entries)
	candidate[id] = &componentSpec{
		constructor:  candidateEntry.constructor,
		dependencies: slices.Clone(candidateEntry.dependencies),
	}

	if _, err := graph.ComputeLevels(dependencyMap(candidate), graph.IgnoreMissingDependencies); err != nil {
		return mapGraphError(err)
	}

	r.entries[id] = candidateEntry
	return nil
}

// Compile validates and freezes the registered dependency graph.
func (r *Registry) Compile() (*Plan, error) {
	if r == nil {
		return nil, fmt.Errorf("registry cannot be nil")
	}

	r.mu.Lock()
	specs := cloneSpecs(r.entries)
	r.mu.Unlock()

	levels, err := graph.ComputeLevels(dependencyMap(specs), graph.StrictValidation)
	if err != nil {
		return nil, mapGraphError(err)
	}

	levelGroups := graph.GroupByLevel(levels)

	return &Plan{
		specs:       specs,
		levelGroups: levelGroups,
	}, nil
}

func mapGraphError(err error) error {
	var cycleErr *graph.CycleError
	if errors.As(err, &cycleErr) {
		if len(cycleErr.Path) > 0 {
			return fmt.Errorf("dependency cycle: %s: %w", strings.Join(cycleErr.Path, " -> "), ErrCyclicDependency)
		}
		return fmt.Errorf("dependency cycle: %w", ErrCyclicDependency)
	}

	var depErr *graph.UnknownDependencyError
	if errors.As(err, &depErr) {
		if depErr.ComponentID != "" {
			return fmt.Errorf("component %q depends on unknown %q: %w", depErr.ComponentID, depErr.DependencyID, ErrNotRegistered)
		}
		return fmt.Errorf("missing dependency %q: %w", depErr.DependencyID, ErrNotRegistered)
	}

	return err
}
