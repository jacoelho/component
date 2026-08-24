package component

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCompileBuildsDeterministicFrontiers(t *testing.T) {
	t.Parallel()

	alpha := newTestNode("alpha")
	beta := newTestNode("beta")
	charlie := newTestNode("charlie")
	delta := newTestNode("delta")
	queen := newTestNode("queen")
	xray := newTestNode("xray")
	zulu := newTestNode("zulu")

	registry := NewRegistry()
	registrations := []struct {
		node testNode
		deps []NodeRef
	}{
		{node: queen, deps: []NodeRef{delta, xray}},
		{node: xray, deps: []NodeRef{charlie}},
		{node: delta, deps: []NodeRef{zulu}},
		{node: zulu, deps: []NodeRef{alpha}},
		{node: charlie, deps: []NodeRef{beta}},
		{node: beta},
		{node: alpha},
	}
	for _, registration := range registrations {
		if err := registry.Register(
			registration.node,
			noOpLifecycle(),
			registration.deps...,
		); err != nil {
			t.Fatalf("Register(%q) failed: %v", registration.node, err)
		}
	}

	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	got := frontierLabels(runtime)
	want := [][]string{
		{"alpha", "beta"},
		{"charlie", "zulu"},
		{"delta", "xray"},
		{"queen"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("frontiers = %v, want %v", got, want)
	}
}

func TestCompileRejectsCyclesWithWitness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		make func(*testing.T, *Registry)
		want []string
	}{
		{
			name: "self",
			make: func(t *testing.T, registry *Registry) {
				t.Helper()

				node := newTestNode("self")
				mustRegister(t, registry, node, node)
			},
			want: []string{"self -> self"},
		},
		{
			name: "three nodes",
			make: func(t *testing.T, registry *Registry) {
				t.Helper()

				a := newTestNode("a")
				b := newTestNode("b")
				c := newTestNode("c")
				mustRegister(t, registry, a, b)
				mustRegister(t, registry, b, c)
				mustRegister(t, registry, c, a)
			},
			want: []string{"a", "b", "c", " -> "},
		},
		{
			name: "after acyclic prefix",
			make: func(t *testing.T, registry *Registry) {
				t.Helper()

				root := newTestNode("root")
				leaf := newTestNode("leaf")
				cycleA := newTestNode("cycle-a")
				cycleB := newTestNode("cycle-b")
				mustRegister(t, registry, root)
				mustRegister(t, registry, leaf, root)
				mustRegister(t, registry, cycleA, cycleB)
				mustRegister(t, registry, cycleB, cycleA)
			},
			want: []string{"cycle-a", "cycle-b", " -> "},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			registry := NewRegistry()
			test.make(t, registry)
			_, err := registry.Compile()
			if !errors.Is(err, ErrCyclicDependency) {
				t.Fatalf("Compile() error = %v, want ErrCyclicDependency", err)
			}
			for _, text := range test.want {
				if !strings.Contains(err.Error(), text) {
					t.Errorf("Compile() error %q does not contain %q", err, text)
				}
			}
		})
	}
}

func TestCompileHandlesVeryDeepGraphsIteratively(t *testing.T) {
	if testing.Short() {
		t.Skip("deep graph")
	}

	const nodeCount = 25_000
	registry := NewRegistry()
	var previous testNode
	for index := range nodeCount {
		node := newTestNode(fmt.Sprintf("node-%05d", index))
		if index == 0 {
			mustRegister(t, registry, node)
		} else {
			mustRegister(t, registry, node, previous)
		}
		previous = node
	}

	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if got := len(runtime.core.frontiers); got != nodeCount {
		t.Fatalf("frontier count = %d, want %d", got, nodeCount)
	}
}

func TestCompileFindsCycleInVeryDeepResidualGraphIteratively(t *testing.T) {
	if testing.Short() {
		t.Skip("deep graph")
	}

	const nodeCount = 20_000
	nodes := make([]testNode, nodeCount)
	for index := range nodes {
		nodes[index] = newTestNode(fmt.Sprintf("cycle-%05d", index))
	}
	registry := NewRegistry()
	for index, node := range nodes {
		dependency := nodes[(index+nodeCount-1)%nodeCount]
		mustRegister(t, registry, node, dependency)
	}

	_, err := registry.Compile()
	if !errors.Is(err, ErrCyclicDependency) {
		t.Fatalf("Compile() error = %v, want ErrCyclicDependency", err)
	}
}

func TestCompileAllowsDistinctNodesWithRepeatedLabels(t *testing.T) {
	t.Parallel()

	first := newTestNode("worker")
	second := newTestNode("worker")
	registry := NewRegistry()
	mustRegister(t, registry, second)
	mustRegister(t, registry, first)

	runtime, err := registry.Compile()
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if len(runtime.core.entries) != 2 {
		t.Fatalf("entry count = %d, want 2", len(runtime.core.entries))
	}
	if runtime.core.entries[0].node.identity == runtime.core.entries[1].node.identity {
		t.Fatal("repeated labels collapsed distinct identities")
	}
	if runtime.core.entries[0].node.ordinal >= runtime.core.entries[1].node.ordinal {
		t.Fatal("equal-label entries are not ordered by creation ordinal")
	}
}

func TestMissingDependencyDiagnosticIgnoresArgumentOrder(t *testing.T) {
	t.Parallel()

	owner := newTestNode("owner")
	alpha := newTestNode("alpha")
	zeta := newTestNode("zeta")
	first := NewRegistry()
	second := NewRegistry()
	mustRegister(t, first, owner, zeta, alpha)
	mustRegister(t, second, owner, alpha, zeta)

	_, firstErr := first.Compile()
	_, secondErr := second.Compile()
	if !errors.Is(firstErr, ErrNotRegistered) || !errors.Is(secondErr, ErrNotRegistered) {
		t.Fatalf("Compile() errors = %v and %v, want ErrNotRegistered", firstErr, secondErr)
	}
	if firstErr.Error() != secondErr.Error() {
		t.Fatalf("missing-dependency diagnostics differ: %q != %q", firstErr, secondErr)
	}
	if !strings.Contains(firstErr.Error(), "alpha") {
		t.Fatalf("diagnostic %q did not select least missing node", firstErr)
	}
}

func BenchmarkCompileGraph(b *testing.B) {
	const nodeCount = 1_000
	benchmarks := []struct {
		name        string
		definitions []graphDefinition
	}{
		{name: "deep-chain", definitions: benchmarkChainDefinitions(nodeCount)},
		{name: "wide", definitions: benchmarkWideDefinitions(nodeCount)},
	}

	for _, benchmark := range benchmarks {
		b.Run(benchmark.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				graph, err := compileGraph(benchmark.definitions)
				if err != nil {
					b.Fatalf("compileGraph() failed: %v", err)
				}
				if len(graph.entries) != nodeCount || len(graph.frontiers) == 0 {
					b.Fatalf(
						"compileGraph() returned %d entries and %d frontiers",
						len(graph.entries),
						len(graph.frontiers),
					)
				}
			}
		})
	}
}

func benchmarkChainDefinitions(count int) []graphDefinition {
	nodes := benchmarkNodeDescriptors(count)
	definitions := make([]graphDefinition, count)
	for index, node := range nodes {
		definitions[index] = graphDefinition{node: node}
		if index != 0 {
			definitions[index].dependencies = []nodeDescriptor{nodes[index-1]}
		}
	}
	return definitions
}

func benchmarkWideDefinitions(count int) []graphDefinition {
	nodes := benchmarkNodeDescriptors(count)
	definitions := make([]graphDefinition, count)
	for index, node := range nodes {
		definitions[index] = graphDefinition{node: node}
	}
	return definitions
}

func benchmarkNodeDescriptors(count int) []nodeDescriptor {
	descriptors := make([]nodeDescriptor, count)
	for index := range descriptors {
		descriptors[index] = descriptor(newTestNode(fmt.Sprintf("node-%05d", index)))
	}
	return descriptors
}

func frontierLabels(runtime *Runtime) [][]string {
	labels := make([][]string, len(runtime.core.frontiers))
	for frontierIndex, frontier := range runtime.core.frontiers {
		labels[frontierIndex] = make([]string, len(frontier))
		for nodeIndex, index := range frontier {
			labels[frontierIndex][nodeIndex] = runtime.core.entries[index].node.label
		}
	}
	return labels
}

func mustRegister(t *testing.T, registry *Registry, node testNode, deps ...NodeRef) {
	t.Helper()
	if err := registry.Register(node, noOpLifecycle(), deps...); err != nil {
		t.Fatalf("Register(%q) failed: %v", node, err)
	}
}
