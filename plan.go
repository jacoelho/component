package component

import (
	"fmt"
	"slices"
	"strings"

	"github.com/jacoelho/component/internal/runtime"
)

// Plan is an immutable compiled dependency graph.
type Plan struct {
	specs       map[string]*componentSpec
	levelGroups [][]string
}

// NewRuntime creates a fresh runtime instance for this plan.
func (p *Plan) NewRuntime() *Runtime {
	rt := &Runtime{
		entries: make(map[string]*runtimeEntry),
		state:   runtime.StateIdle,
		fsm:     runtime.NewLifecycleFSM(),
	}

	if p == nil {
		return rt
	}

	rt.levelGroups = make([][]string, len(p.levelGroups))

	for id, spec := range p.specs {
		rt.entries[id] = &runtimeEntry{
			constructor: spec.constructor,
		}
	}

	for level, ids := range p.levelGroups {
		rt.levelGroups[level] = slices.Clone(ids)
	}

	return rt
}

// DotGraph outputs the plan dependency graph in Graphviz DOT format.
func (p *Plan) DotGraph() string {
	var b strings.Builder
	b.WriteString("digraph G {\n  rankdir=TB;\n  compound=true;\n")

	if p == nil {
		b.WriteString("}\n")
		return b.String()
	}

	nodeMap := make(map[string]string, len(p.specs))
	for level, componentIDs := range p.levelGroups {
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

	componentIDs := make([]string, 0, len(p.specs))
	for id := range p.specs {
		componentIDs = append(componentIDs, id)
	}
	slices.Sort(componentIDs)

	for _, componentID := range componentIDs {
		spec := p.specs[componentID]
		toNode := nodeMap[componentID]

		dependencies := slices.Clone(spec.dependencies)
		slices.Sort(dependencies)

		for _, dependency := range dependencies {
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
	return b.String()
}
