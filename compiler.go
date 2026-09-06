package component

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
)

type graphEntry struct {
	definition *definition
	arguments  []int
	dependents []int
}

// New validates the roots' dependency closure and fixes a serial execution order
// without invoking constructors.
// Repeated roots and shared inputs identify the same instance within this runtime.
func New(roots ...Root) (*Runtime, error) {
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
	remaining := make([]int, len(entries))
	for index := range entries {
		for _, input := range entries[index].definition.inputs {
			dependency := indices[input]
			if !slices.Contains(entries[index].arguments, dependency) {
				remaining[index]++
				entries[dependency].dependents = append(entries[dependency].dependents, index)
			}
			entries[index].arguments = append(entries[index].arguments, dependency)
		}
	}
	// Compute the order once; startup walks it forward and cleanup walks it backward.
	var order []int
	for index := range entries {
		if remaining[index] == 0 {
			order = append(order, index)
		}
	}
	for next := 0; next < len(order); next++ {
		for _, dependent := range entries[order[next]].dependents {
			remaining[dependent]--
			if remaining[dependent] == 0 {
				order = append(order, dependent)
			}
		}
	}
	if len(order) != len(entries) {
		return nil, fmt.Errorf("%w: dependency cycle", ErrInvalidDefinition)
	}
	return &Runtime{core: &runtimeCore{
		entries: entries, indices: indices, order: order,
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
