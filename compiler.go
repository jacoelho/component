package component

import (
	"fmt"
	"sort"
	"strings"
)

// Compile validates the graph and transfers lifecycle ownership into one
// Runtime. A successful Compile consumes the Registry. A failed Compile leaves
// it editable.
func (r *Registry) Compile() (*Runtime, error) {
	if r == nil {
		return nil, fmt.Errorf("component: compile on nil registry")
	}

	core := r.ensureCore()
	core.mu.Lock()
	defer core.mu.Unlock()

	if core.consumed {
		return nil, ErrRegistryConsumed
	}

	declarations := make([]declaration, 0, len(core.declarations))
	for _, declared := range core.declarations {
		declarations = append(declarations, cloneDeclaration(declared))
	}
	sort.Slice(declarations, func(i, j int) bool {
		return descriptorLess(declarations[i].node, declarations[j].node)
	})

	entries, frontiers, err := compileDeclarations(declarations)
	if err != nil {
		return nil, err
	}

	runtime := &Runtime{core: &runtimeCore{
		entries:   entries,
		frontiers: frontiers,
		cleanup:   make([]bool, len(entries)),
		state:     runtimeIdle,
	}}
	core.declarations = nil
	core.consumed = true
	return runtime, nil
}

func cloneDeclaration(declared declaration) declaration {
	return declaration{
		node:         declared.node,
		lifecycle:    declared.lifecycle,
		dependencies: append([]nodeDescriptor(nil), declared.dependencies...),
	}
}

func compileDeclarations(
	declarations []declaration,
) ([]runtimeEntry, [][]int, error) {
	entries := make([]runtimeEntry, len(declarations))
	indices := make(map[*nodeIdentity]int, len(declarations))
	for index, declared := range declarations {
		indices[declared.node.identity] = index
		entries[index] = runtimeEntry{
			node:      declared.node,
			lifecycle: declared.lifecycle,
		}
	}

	for index := range declarations {
		declared := &declarations[index]
		dependencyDescriptors := declared.dependencies
		sort.Slice(dependencyDescriptors, func(i, j int) bool {
			return descriptorLess(dependencyDescriptors[i], dependencyDescriptors[j])
		})
		dependencies := make([]int, 0, len(dependencyDescriptors))
		for _, dependency := range dependencyDescriptors {
			dependencyIndex, exists := indices[dependency.identity]
			if !exists {
				return nil, nil, fmt.Errorf(
					"%w: node %q depends on %q",
					ErrNotRegistered,
					declared.node.label,
					dependency.label,
				)
			}
			dependencies = append(dependencies, dependencyIndex)
			entries[dependencyIndex].dependents = append(
				entries[dependencyIndex].dependents,
				index,
			)
		}
		sort.Ints(dependencies)
		entries[index].dependencies = dependencies
	}
	for index := range entries {
		sort.Ints(entries[index].dependents)
	}

	frontiers, residual := kahnFrontiers(entries)
	if hasResidual(residual) {
		witness := cycleWitness(entries, residual)
		return nil, nil, fmt.Errorf("%w: %s", ErrCyclicDependency, witness)
	}
	return entries, frontiers, nil
}

func hasResidual(residual []bool) bool {
	for _, present := range residual {
		if present {
			return true
		}
	}
	return false
}

func descriptorLess(left, right nodeDescriptor) bool {
	if left.label != right.label {
		return left.label < right.label
	}
	return left.ordinal < right.ordinal
}

func kahnFrontiers(entries []runtimeEntry) ([][]int, []bool) {
	remainingDependencies := make([]int, len(entries))
	order := make([]int, 0, len(entries))
	for index := range entries {
		remainingDependencies[index] = len(entries[index].dependencies)
		if remainingDependencies[index] == 0 {
			order = append(order, index)
		}
	}

	frontiers := make([][]int, 0)
	processed := make([]bool, len(entries))
	for start := 0; start < len(order); {
		end := len(order)
		current := order[start:end:end]
		frontiers = append(frontiers, current)
		for _, dependency := range current {
			processed[dependency] = true
			for _, dependent := range entries[dependency].dependents {
				remainingDependencies[dependent]--
				if remainingDependencies[dependent] == 0 {
					order = append(order, dependent)
				}
			}
		}
		sort.Ints(order[end:])
		start = end
	}

	residual := make([]bool, len(entries))
	for index := range entries {
		residual[index] = !processed[index]
	}
	return frontiers, residual
}

type dfsFrame struct {
	node int
	next int
}

func cycleWitness(entries []runtimeEntry, residual []bool) string {
	const (
		unvisited uint8 = iota
		visiting
		visited
	)
	colors := make([]uint8, len(entries))
	positions := make([]int, len(entries))
	for index := range positions {
		positions[index] = -1
	}

	for root := range entries {
		if !residual[root] || colors[root] != unvisited {
			continue
		}
		stack := []dfsFrame{{node: root}}
		path := []int{root}
		colors[root] = visiting
		positions[root] = 0

		for len(stack) != 0 {
			frame := &stack[len(stack)-1]
			if frame.next == len(entries[frame.node].dependencies) {
				colors[frame.node] = visited
				positions[frame.node] = -1
				stack = stack[:len(stack)-1]
				path = path[:len(path)-1]
				continue
			}

			dependency := entries[frame.node].dependencies[frame.next]
			frame.next++
			if !residual[dependency] {
				continue
			}
			switch colors[dependency] {
			case unvisited:
				colors[dependency] = visiting
				positions[dependency] = len(path)
				path = append(path, dependency)
				stack = append(stack, dfsFrame{node: dependency})
			case visiting:
				cycle := append([]int(nil), path[positions[dependency]:]...)
				cycle = append(cycle, dependency)
				return formatCycle(entries, cycle)
			}
		}
	}
	return "cycle detected"
}

func formatCycle(entries []runtimeEntry, cycle []int) string {
	labels := make([]string, len(cycle))
	for index, nodeIndex := range cycle {
		label := entries[nodeIndex].node.label
		if label == "" {
			label = "<unnamed>"
		}
		labels[index] = label
	}
	return strings.Join(labels, " -> ")
}
