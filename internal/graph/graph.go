package graph

import (
	"fmt"
	"slices"
	"strings"
)

// Mode controls how missing dependencies are handled during level computation.
type Mode int

const (
	StrictValidation Mode = iota
	IgnoreMissingDependencies
)

// UnknownDependencyError indicates a dependency reference to a non-registered component.
type UnknownDependencyError struct {
	ComponentID  string
	DependencyID string
}

func (e *UnknownDependencyError) Error() string {
	if e.ComponentID == "" {
		return fmt.Sprintf("unknown dependency %q", e.DependencyID)
	}
	return fmt.Sprintf("component %q depends on unknown %q", e.ComponentID, e.DependencyID)
}

// CycleError indicates a cycle in the dependency graph.
type CycleError struct {
	Path []string
}

func (e *CycleError) Error() string {
	if len(e.Path) == 0 {
		return "dependency cycle"
	}
	return fmt.Sprintf("dependency cycle: %s", strings.Join(e.Path, " -> "))
}

type visitState int

const (
	unvisited visitState = iota
	visiting
	visited
)

// ComputeLevels calculates dependency depth for each component.
func ComputeLevels(entries map[string][]string, mode Mode) (map[string]int, error) {
	if len(entries) == 0 {
		return map[string]int{}, nil
	}

	levels := make(map[string]int, len(entries))
	colors := make(map[string]visitState, len(entries))

	var visit func(string, []string) (int, error)
	visit = func(id string, path []string) (int, error) {
		deps, exists := entries[id]
		if !exists {
			if mode == IgnoreMissingDependencies {
				return 0, nil
			}
			return 0, &UnknownDependencyError{DependencyID: id}
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

		for _, dep := range deps {
			if _, depExists := entries[dep]; !depExists {
				if mode == IgnoreMissingDependencies {
					maxDepth = max(maxDepth, 1)
					continue
				}
				return 0, &UnknownDependencyError{ComponentID: id, DependencyID: dep}
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

// GroupByLevel groups component IDs by dependency level in ascending order.
func GroupByLevel(levels map[string]int) [][]string {
	if len(levels) == 0 {
		return nil
	}

	highestLevel := 0
	for _, level := range levels {
		if level > highestLevel {
			highestLevel = level
		}
	}

	levelGroups := make([][]string, highestLevel+1)
	for componentID, level := range levels {
		if level < 0 {
			level = 0
		}
		levelGroups[level] = append(levelGroups[level], componentID)
	}

	for level := range levelGroups {
		slices.Sort(levelGroups[level])
	}

	return levelGroups
}

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
		return &CycleError{Path: cyclePath}
	}

	cyclePath := append(append([]string{}, path...), id)
	return &CycleError{Path: cyclePath}
}
