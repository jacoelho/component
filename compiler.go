package component

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// RuntimeOptions bounds concurrent constructor and lifecycle invocations.
type RuntimeOptions struct {
	// Parallelism defaults to one when zero. Negative values are invalid.
	Parallelism int
}

type graphEntry struct {
	definition   *definition
	arguments    []int
	dependencies []int
	dependents   []int
}

// New validates the roots' dependency closure without invoking constructors.
// Repeated roots and shared inputs identify the same instance within this runtime.
func New(options RuntimeOptions, roots ...Root) (*Runtime, error) {
	if options.Parallelism < 0 {
		return nil, ErrInvalidOptions
	}
	parallelism := options.Parallelism
	if parallelism == 0 {
		parallelism = 1
	}
	indices := make(map[*definition]int)
	var entries []graphEntry
	var failures []error
	// Reverse pushes preserve root/input preorder without recursive traversal.
	stack := make([]*definition, 0, len(roots))
	for i := len(roots) - 1; i >= 0; i-- {
		stack = append(stack, rootDefinition(roots[i]))
	}
	for len(stack) > 0 {
		last := len(stack) - 1
		node := stack[last]
		stack = stack[:last]
		if node == nil {
			failures = append(failures, ErrInvalidReference)
			continue
		}
		if _, exists := indices[node]; exists {
			continue
		}
		index := len(entries)
		indices[node] = index
		entries = append(entries, graphEntry{definition: node})
		if node.err != nil {
			failures = append(failures, nodeFailure(index, node, "declare", node.err))
		}
		for i := len(node.inputs) - 1; i >= 0; i-- {
			stack = append(stack, node.inputs[i])
		}
	}
	if len(failures) != 0 {
		return nil, errors.Join(failures...)
	}
	for index := range entries {
		for _, input := range entries[index].definition.inputs {
			dependency := indices[input]
			entries[index].arguments = append(entries[index].arguments, dependency)
			if slices.Contains(entries[index].dependencies, dependency) {
				continue
			}
			entries[index].dependencies = append(entries[index].dependencies, dependency)
			entries[dependency].dependents = append(entries[dependency].dependents, index)
		}
	}
	// Public refs cannot form cycles; retain a defensive check at the graph boundary.
	remaining := make([]int, len(entries))
	var ready []int
	for index := range entries {
		remaining[index] = len(entries[index].dependencies)
		if remaining[index] == 0 {
			ready = append(ready, index)
		}
	}
	for next := 0; next < len(ready); next++ {
		for _, dependent := range entries[ready[next]].dependents {
			remaining[dependent]--
			if remaining[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(ready) != len(entries) {
		return nil, fmt.Errorf("%w: dependency cycle", ErrInvalidDefinition)
	}
	// A larger buffer cannot create useful concurrency beyond the reachable graph.
	parallelism = min(parallelism, max(1, len(entries)))
	return &Runtime{core: &runtimeCore{
		entries: entries, indices: indices, parallelism: parallelism,
		values: make([]any, len(entries)),
	}}, nil
}

func rootDefinition(root Root) (node *definition) {
	// A nil *Ref or an embedded nil ref must be a validation error, not a panic.
	defer func() {
		if recover() != nil {
			node = nil
		}
	}()
	if root == nil {
		return nil
	}
	value := reflect.ValueOf(root)
	if value.Kind() == reflect.Pointer && value.IsNil() {
		return nil
	}
	return root.definition()
}
