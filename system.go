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

	// ErrPanic is returned when multiple component initialization or shutdown panics
	ErrPanic = errors.New("component panicked")
)

// wrapComponentError wraps an error with component context information.
func wrapComponentError(componentID string, operation string, err error) error {
	return fmt.Errorf("component %q %s: %w", componentID, operation, err)
}

// wrapRegistrationError wraps errors specific to component registration.
func wrapRegistrationError(componentID string, err error) error {
	return fmt.Errorf("component %q already registered: %w", componentID, err)
}

// wrapRetrievalError wraps errors specific to component retrieval.
func wrapRetrievalError(componentID string, err error) error {
	return fmt.Errorf("component %q not registered: %w", componentID, err)
}

// errorCollector safely collects errors from concurrent operations.
type errorCollector struct {
	mu   sync.Mutex
	errs []error
}

func newErrorCollector() *errorCollector {
	return &errorCollector{
		errs: make([]error, 0),
	}
}

// append adds an error to the collection in a thread-safe manner.
func (ec *errorCollector) append(err error) {
	if err == nil {
		return
	}
	ec.mu.Lock()
	ec.errs = append(ec.errs, err)
	ec.mu.Unlock()
}

func (ec *errorCollector) appendf(format string, args ...any) {
	ec.append(fmt.Errorf(format, args...))
}

// errors returns all collected errors as a joined error, or nil if no errors.
func (ec *errorCollector) errors() error {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	if len(ec.errs) == 0 {
		return nil
	}
	return errors.Join(ec.errs...)
}

func (ec *errorCollector) hasErrors() bool {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return len(ec.errs) > 0
}

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
	deps ...Keyer,
) error {
	if sys == nil {
		return fmt.Errorf("system cannot be nil")
	}
	if fn == nil {
		return fmt.Errorf("constructor function cannot be nil")
	}

	sys.mu.Lock()
	defer sys.mu.Unlock()

	if sys.entries == nil {
		sys.entries = make(map[string]*entry)
	}

	id := key.id()
	if _, exists := sys.entries[id]; exists {
		return wrapRegistrationError(id, ErrAlreadyRegistered)
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
	sys.entries[id] = &entry{
		constructor: func(s *System) (any, error) {
			return fn(sys)
		},
		dependencies: dependencies,
	}

	if _, err := computeLevels(sys.entries, ignoreMissingDeps); err != nil {
		return err
	}

	return nil
}

// ProvideWithoutKey registers a component of type T with an auto-generated key.
// Useful for components without dependents that don't need explicit key management.
func ProvideWithoutKey[T Lifecycle](
	sys *System,
	fn func(*System) (T, error),
	deps ...Keyer,
) error {
	return Provide(sys, Key[T]{}, fn, deps...)
}

// Get returns the already-started T.
func Get[T Lifecycle](sys *System, key Key[T]) (T, error) {
	var zero T

	if sys == nil {
		return zero, fmt.Errorf("system cannot be nil")
	}

	id := key.id()
	if id == "" {
		return zero, fmt.Errorf("component key cannot be empty")
	}

	sys.mu.Lock()
	defer sys.mu.Unlock()

	ent, ok := sys.entries[id]
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

// Start brings up your components level‐by‐level, rolling back on errors.
func (sys *System) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("context cannot be nil")
	}

	sys.mu.Lock()

	if err := walkDependencies(sys.entries); err != nil {
		sys.mu.Unlock()
		return err
	}

	levels, err := computeLevels(sys.entries, strictValidation)
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
	if ctx == nil {
		return fmt.Errorf("context cannot be nil")
	}

	sys.mu.Lock()
	levels, err := computeLevels(sys.entries, strictValidation)
	if err != nil {
		sys.mu.Unlock()
		return err
	}
	sys.mu.Unlock()

	groups, maxLevel := groupByLevel(levels)

	var errs []error
	for level := maxLevel; level >= 0; level-- {
		if err := sys.stopLevel(ctx, groups[level]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (sys *System) startLevel(ctx context.Context, ids []string) error {
	ec := newErrorCollector()

	// Extract entry data before starting goroutines to avoid race conditions
	type entryData struct {
		id          string
		constructor func(*System) (any, error)
	}

	sys.mu.Lock()
	entries := make([]entryData, 0, len(ids))
	for _, id := range ids {
		if ent, exists := sys.entries[id]; exists {
			entries = append(entries, entryData{
				id:          id,
				constructor: ent.constructor,
			})
		} else {
			ec.appendf("missing entry for type %s: %w", id, ErrNotRegistered)
		}
	}
	sys.mu.Unlock()

	var wg sync.WaitGroup
	for _, entry := range entries {
		wg.Add(1)
		go func(entry entryData) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					ec.appendf("panic during start for %q %v: %w", entry.id, r, ErrPanic)
				}
			}()

			instance, err := entry.constructor(sys)
			if err != nil {
				ec.appendf("provide for %q: %w", entry.id, err)
				return
			}
			lc, ok := instance.(Lifecycle)
			if !ok {
				ec.appendf("%T does not implement Lifecycle", instance)
				return
			}
			if err := lc.Start(ctx); err != nil {
				ec.appendf("start failed for %q: %w", entry.id, err)
				return
			}

			sys.mu.Lock()
			defer sys.mu.Unlock()
			if ent, exists := sys.entries[entry.id]; exists {
				if ent.instance != nil {
					ec.appendf("duplicate initialization of %q: %w", entry.id, ErrAlreadyInitialized)
					return
				}
				ent.instance = instance
			}
		}(entry)
	}
	wg.Wait()
	return ec.errors()
}

func (sys *System) stopLevel(ctx context.Context, ids []string) error {
	ec := newErrorCollector()

	// Extract instance data before starting goroutines to avoid race conditions
	type instanceData struct {
		id        string
		lifecycle Lifecycle
	}

	sys.mu.Lock()
	instances := make([]instanceData, 0, len(ids))
	for _, id := range ids {
		ent, exists := sys.entries[id]
		if !exists {
			ec.appendf("missing entry for type %q: %w", id, ErrNotRegistered)
			continue
		}

		if ent.instance == nil {
			continue
		}

		lc, ok := ent.instance.(Lifecycle)
		if !ok {
			ec.appendf("%T does not implement Lifecycle", ent.instance)
			continue
		}

		instances = append(instances, instanceData{
			id:        id,
			lifecycle: lc,
		})
		ent.instance = nil
	}
	sys.mu.Unlock()

	var wg sync.WaitGroup
	for _, inst := range instances {
		wg.Add(1)
		go func(inst instanceData) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					ec.appendf("panic during stop for %q %v: %w", inst.id, r, ErrPanic)
				}
			}()

			if err := inst.lifecycle.Stop(ctx); err != nil {
				ec.appendf("stop failed for %q: %w", inst.id, err)
			}
		}(inst)
	}

	wg.Wait()
	return ec.errors()
}

// DotGraph outputs the system's dependency graph in Graphviz DOT format.
// The graph shows components grouped by dependency levels and their relationships.
func (sys *System) DotGraph() (string, error) {
	sys.mu.Lock()
	defer sys.mu.Unlock()

	levels, err := computeLevels(sys.entries, strictValidation)
	if err != nil {
		return "", err
	}

	levelGroups, highestLevel := groupByLevel(levels)

	estimatedSize := len(sys.entries)*50 + 200 // rough estimate
	var b strings.Builder
	b.Grow(estimatedSize)

	b.WriteString("digraph G {\n  rankdir=TB;\n  compound=true;\n")

	nodeMap := make(map[string]string, len(sys.entries))
	for level := 0; level <= highestLevel; level++ {
		componentIDs := levelGroups[level]
		if len(componentIDs) == 0 {
			continue
		}

		b.WriteString("  subgraph cluster_")
		b.WriteString(fmt.Sprintf("%d", level))
		b.WriteString(" {\n    label=\"Level ")
		b.WriteString(fmt.Sprintf("%d", level))
		b.WriteString("\";\n    style=dashed;\n")

		for _, id := range componentIDs {
			quotedName := fmt.Sprintf("%q", id)
			nodeMap[id] = quotedName
			b.WriteString("    ")
			b.WriteString(quotedName)
			b.WriteString(";\n")
		}
		b.WriteString("  }\n")
	}

	for componentID, entry := range sys.entries {
		toNode := nodeMap[componentID]
		for _, dependency := range entry.dependencies {
			if fromNode, exists := nodeMap[dependency]; exists {
				b.WriteString("  ")
				b.WriteString(fromNode)
				b.WriteString(" -> ")
				b.WriteString(toNode)
				b.WriteString(";\n")
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
				return fmt.Errorf("component %q depends on unknown %q: %w", typ, dep, ErrNotRegistered)
			}
		}
	}
	return nil
}

// visitState represents the state of a node during DFS traversal
type visitState int

const (
	unvisited visitState = iota
	visiting
	visited
)

// validationMode controls how missing dependencies are handled during level computation
type validationMode int

const (
	strictValidation validationMode = iota
	ignoreMissingDeps
)

// buildCycleError constructs a detailed cycle error message
func buildCycleError(id string, path []string) error {
	cycleStart := -1
	for i, pathID := range path {
		if pathID == id {
			cycleStart = i
			break
		}
	}
	if cycleStart >= 0 {
		cyclePath := append(path[cycleStart:], id)
		return fmt.Errorf("dependency cycle: %s: %w",
			strings.Join(cyclePath, " -> "), ErrCyclicDependency)
	}
	return fmt.Errorf("dependency cycle involving %q: %w", id, ErrCyclicDependency)
}

// computeLevels calculates the dependency depth for each component using recursive DFS.
// Missing dependencies are treated as level 0 leaves when mode is ignoreMissingDeps.
func computeLevels(
	entries map[string]*entry,
	mode validationMode,
) (map[string]int, error) {
	if len(entries) == 0 {
		return make(map[string]int), nil
	}

	levels := make(map[string]int, len(entries))
	colors := make(map[string]visitState, len(entries))

	var visit func(string, []string) (int, error)
	visit = func(id string, path []string) (int, error) {
		ent, exists := entries[id]
		if !exists {
			if mode == ignoreMissingDeps {
				return 0, nil // Treat as leaf node
			}
			return 0, fmt.Errorf("missing dependency %q: %w", id, ErrNotRegistered)
		}

		switch colors[id] {
		case visiting:
			return 0, buildCycleError(id, path)
		case visited:
			return levels[id], nil
		}

		colors[id] = visiting
		maxDepth := 0
		currentPath := append(path, id)

		for _, dep := range ent.dependencies {
			if _, exists := entries[dep]; !exists && mode == ignoreMissingDeps {
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

// groupByLevel groups component IDs by their dependency level, returning
// a map of level to sorted component IDs and the highest level found.
func groupByLevel(levels map[string]int) (map[int][]string, int) {
	if levels == nil {
		return make(map[int][]string), 0
	}

	levelGroups := make(map[int][]string, len(levels))
	highestLevel := 0

	for componentID, level := range levels {
		if level < 0 {
			level = 0 // Defensive: ensure non-negative levels
		}
		levelGroups[level] = append(levelGroups[level], componentID)
		if level > highestLevel {
			highestLevel = level
		}
	}

	// Sort component IDs within each level for determinism
	for level, componentIDs := range levelGroups {
		slices.Sort(componentIDs)
		levelGroups[level] = componentIDs
	}

	return levelGroups, highestLevel
}
