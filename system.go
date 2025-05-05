package component

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

var (
	// ErrCyclicDependency is returned by Provide when registering a component
	// would introduce a cycle into the dependency graph.
	ErrCyclicDependency = errors.New("cyclic dependency")

	// ErrNotRegistered is returned by Get or Start when an operation
	// refers to a component key that has not been provided.
	ErrNotRegistered = errors.New("not registered")

	// ErrAlreadyRegistered is returned by Provide when attempting to register
	// a component key that is already in use.
	ErrAlreadyRegistered = errors.New("already registered")

	// ErrNotStarted is returned by Get when trying to retrieve a component
	// instance that has not yet been started.
	ErrNotStarted = errors.New("not started")

	// ErrIncorrectType is returned by Get when the stored component instance
	// cannot be asserted to the expected type T.
	ErrIncorrectType = errors.New("incorrect type")

	// ErrAlreadyInitialized is returned when multiple component initializations
	// are attempted.
	ErrAlreadyInitialized = errors.New("already initialized")
)

type entry struct {
	constructor  func(*System) (any, error)
	instance     any
	dependencies []string
}

// System starts and stops your components.
type System struct {
	mu      sync.Mutex
	entries map[string]*entry
}

// Provide registers a component of type T.
func Provide[T Lifecycle](
	sys *System,
	key Key[T],
	fn func(*System) (T, error),
	deps ...keyer,
) error {
	sys.mu.Lock()
	defer sys.mu.Unlock()

	if sys.entries == nil {
		sys.entries = make(map[string]*entry)
	}

	id := key.id()
	if _, exists := sys.entries[id]; exists {
		return fmt.Errorf("component %q already registered: %w", id, ErrAlreadyRegistered)
	}

	dependencies := make([]string, len(deps))
	for i, d := range deps {
		dependencies[i] = d.id()
	}
	sys.entries[id] = &entry{
		constructor: func(s *System) (any, error) {
			return fn(sys)
		},
		dependencies: dependencies,
	}

	if _, err := computeLevels(sys.entries, true); err != nil {
		return err
	}

	return nil
}

// ProvideWithoutKey registers a component of type T, it's key is generated
// Useful for components without dependents.
func ProvideWithoutKey[T Lifecycle](
	sys *System,
	fn func(*System) (T, error),
	deps ...keyer,
) error {
	return Provide(sys, Key[T]{}, fn, deps...)
}

// MustProvide registers a component of type T, panics if it errors.
func MustProvide[T Lifecycle](
	sys *System,
	key Key[T],
	fn func(*System) (T, error),
	deps ...keyer,
) {
	if err := Provide(sys, key, fn, deps...); err != nil {
		panic(err)
	}
}

// Get returns the already-started T.
func Get[T Lifecycle](sys *System, key Key[T]) (T, error) {
	var zero T
	id := key.id()

	sys.mu.Lock()
	defer sys.mu.Unlock()

	ent, ok := sys.entries[id]
	if !ok {
		return zero, fmt.Errorf("component %q not registered: %w", id, ErrNotRegistered)
	}
	if ent.instance == nil {
		return zero, fmt.Errorf("component %q not started: %w", id, ErrNotStarted)
	}
	inst, ok := ent.instance.(T)
	if !ok {
		return zero, fmt.Errorf("component %q has wrong type: %w", id, ErrIncorrectType)
	}
	return inst, nil
}

// Start brings up your components level‐by‐level, rolling back on errors.
func (sys *System) Start(ctx context.Context) error {
	sys.mu.Lock()

	if err := walkDependencies(sys.entries); err != nil {
		sys.mu.Unlock()
		return err
	}

	levels, err := computeLevels(sys.entries, false)
	if err != nil {
		sys.mu.Unlock()
		return err
	}

	sys.mu.Unlock()

	groups, maxLevel := groupByLevel(levels)
	for level := 0; level <= maxLevel; level++ {
		if err := sys.startLevel(ctx, groups[level]); err != nil {
			return errors.Join(err, sys.Stop(ctx))
		}
	}

	return nil
}

// Stop tears down in descending‐level order, parallel within each level.
func (sys *System) Stop(ctx context.Context) error {
	levels, err := computeLevels(sys.entries, false)
	if err != nil {
		return err
	}

	groups, maxLevel := groupByLevel(levels)
	for level := maxLevel; level >= 0; level-- {
		if err := sys.stopLevel(ctx, groups[level]); err != nil {
			return err
		}
	}

	return nil
}

// startLevel starts all entries in parallel and returns combined errors.
func (sys *System) startLevel(ctx context.Context, ids []string) error {
	ec := newErrCollector()

	var wg sync.WaitGroup
	for _, id := range ids {
		sys.mu.Lock()
		ent, exists := sys.entries[id]
		sys.mu.Unlock()

		if !exists {
			ec.appendf("missing entry for type %s: %w", id, ErrNotRegistered)
			continue
		}

		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			instance, err := ent.constructor(sys)
			if err != nil {
				ec.appendf("provide for %q: %w", id, err)
				return
			}
			lc, ok := instance.(Lifecycle)
			if !ok {
				ec.appendf("%T does not implement Lifecycle", instance)
				return
			}
			if err := lc.Start(ctx); err != nil {
				ec.appendf("start failed for %q: %w", id, err)
				return
			}
			sys.mu.Lock()
			defer sys.mu.Unlock()
			if ent.instance != nil {
				ec.appendf("duplicate initialization of %q: %w", id, ErrAlreadyInitialized)
				return
			}
			ent.instance = instance
		}(id)
	}
	wg.Wait()
	return ec.errors()
}

// stopLevel stops all entries in parallel and returns combined errors.
func (sys *System) stopLevel(ctx context.Context, ids []string) error {
	ec := newErrCollector()

	var wg sync.WaitGroup
	for _, id := range ids {
		sys.mu.Lock()
		ent, exists := sys.entries[id]
		var inst any
		if exists {
			inst = ent.instance
			ent.instance = nil
		}
		sys.mu.Unlock()

		if !exists {
			ec.appendf("missing entry for type %q: %w", id, ErrNotRegistered)
			continue
		}

		if inst == nil {
			continue
		}

		lc, ok := inst.(Lifecycle)
		if !ok {
			ec.appendf("%T does not implement Lifecycle", inst)
			continue
		}

		wg.Add(1)
		go func(lc Lifecycle, id string) {
			defer wg.Done()

			if err := lc.Stop(ctx); err != nil {
				ec.appendf("stop failed for %q: %w", id, err)
			}
		}(lc, id)
	}

	wg.Wait()
	return ec.errors()
}

// DotGraph outputs the system's dependency graph in dot format.
func (sys *System) DotGraph() (string, error) {
	sys.mu.Lock()
	defer sys.mu.Unlock()

	levels, err := computeLevels(sys.entries, false)
	if err != nil {
		return "", err
	}

	groups, maxLevel := groupByLevel(levels)
	var b strings.Builder

	b.WriteString("digraph G {\n  rankdir=TB;\n  compound=true;\n")

	nodeMap := make(map[string]string)
	for lvl := 0; lvl <= maxLevel; lvl++ {
		ids := groups[lvl]
		if len(ids) == 0 {
			continue
		}

		fmt.Fprintf(&b, "  subgraph cluster_%d {\n", lvl)
		fmt.Fprintf(&b, "    label=\"Level %d\";\n    style=dashed;\n", lvl)

		for _, id := range ids {
			name := fmt.Sprintf("%q", id)
			nodeMap[id] = name
			fmt.Fprintf(&b, "    %s;\n", name)
		}
		b.WriteString("  }\n")
	}

	for id, ent := range sys.entries {
		toName := nodeMap[id]
		for _, dep := range ent.dependencies {
			if fromName, exists := nodeMap[dep]; exists {
				fmt.Fprintf(&b, "  %s -> %s;\n", fromName, toName)
			}
		}
	}

	b.WriteString("}\n")
	return b.String(), nil
}

func walkDependencies(entries map[string]*entry) error {
	for typ, ent := range entries {
		for _, dep := range ent.dependencies {
			if _, ok := entries[dep]; !ok {
				return fmt.Errorf("component %v depends on unknown %v: %w", typ, dep, ErrNotRegistered)
			}
		}
	}
	return nil
}

// computeLevels calculates the dependency depth for each component
// missing dependencies are treated as level 0 leaves when ignoreMissing=true.
func computeLevels(
	entries map[string]*entry,
	ignoreMissing bool,
) (map[string]int, error) {
	const (
		unvisited = iota
		visiting
		visited
	)

	levels := make(map[string]int, len(entries))
	colors := make(map[string]int, len(entries))

	var visit func(string, []string) (int, error)
	visit = func(id string, path []string) (int, error) {
		ent, exists := entries[id]
		if !exists {
			if ignoreMissing {
				return 0, nil // Treat as leaf node
			}
			return 0, fmt.Errorf("missing dependency %q: %w", id, ErrNotRegistered)
		}

		switch colors[id] {
		case visiting:
			return 0, fmt.Errorf("dependency cycle: %s: %w", formatCycle(path, id), ErrCyclicDependency)
		case visited:
			return levels[id], nil
		}

		colors[id] = visiting
		maxDepth := 0
		currentPath := append(path, id)

		for _, dep := range ent.dependencies {
			if _, exists := entries[dep]; !exists && ignoreMissing {
				maxDepth = max(maxDepth, 1)
				continue
			}

			depth, err := visit(dep, currentPath)
			if err != nil {
				return 0, err
			}
			maxDepth = max(maxDepth, depth+1)
		}

		levels[id] = maxDepth
		colors[id] = visited
		return maxDepth, nil
	}

	for id := range entries {
		if colors[id] == unvisited {
			if _, err := visit(id, nil); err != nil {
				return nil, err
			}
		}
	}

	return levels, nil
}

// formatCycle creates a human-readable cycle path
func formatCycle(path []string, cycleEnd string) string {
	var b strings.Builder
	for i, t := range path {
		if t == cycleEnd && i > 0 {
			fmt.Fprintf(&b, "%v -> %v [CYCLE]", path[:i], cycleEnd)
			return b.String()
		}
	}
	return fmt.Sprintf("%v -> %v", path, cycleEnd)
}

// groupByLevel groups types by their level from the input map, returning
// a map of level to types slice and the highest level found.
// Each slice is sorted for determinism.
func groupByLevel(levels map[string]int) (map[int][]string, int) {
	groups := make(map[int][]string, len(levels))
	maxLevel := 0
	for id, level := range levels {
		groups[level] = append(groups[level], id)
		if level > maxLevel {
			maxLevel = level
		}
	}
	for level := range groups {
		slice := groups[level]
		slices.Sort(slice)
		groups[level] = slice
	}
	return groups, maxLevel
}

type errCollector struct {
	mu   sync.Mutex
	errs []error
}

func newErrCollector() *errCollector {
	return &errCollector{
		errs: make([]error, 0),
	}
}

func (ec *errCollector) append(err error) {
	ec.mu.Lock()
	ec.errs = append(ec.errs, err)
	ec.mu.Unlock()
}

func (ec *errCollector) appendf(format string, args ...any) {
	ec.append(fmt.Errorf(format, args...))
}

func (ec *errCollector) errors() error {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return errors.Join(ec.errs...)
}
