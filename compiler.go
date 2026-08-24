package component

import (
	"fmt"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
)

type ownerDefinition struct {
	node         nodeDescriptor
	dependencies []nodeDescriptor
	lifecycle    Lifecycle
	value        reflect.Value
	provider     providerEntry
	provided     bool
	arguments    []int
}

type graphDefinition struct {
	node         nodeDescriptor
	dependencies []nodeDescriptor
}

type graphEntry struct {
	node         nodeDescriptor
	dependencies []int
	dependents   []int
}

type compiledGraph struct {
	entries   []graphEntry
	frontiers [][]int
}

// Compile validates the complete graph before invoking any provider
// constructor. Structural failures leave the Registry editable. Once
// construction begins, the Registry is consumed even if construction fails.
func (r *Registry) Compile() (*Runtime, error) {
	if r == nil {
		return nil, fmt.Errorf("component: compile on nil registry")
	}

	core := r.ensureCore()
	core.mu.Lock()
	if core.consumed {
		core.mu.Unlock()
		return nil, ErrRegistryConsumed
	}

	definitions := snapshotOwners(core)
	graph, err := compileOwners(definitions, core.bindings)
	if err != nil {
		core.mu.Unlock()
		return nil, err
	}

	core.declarations = nil
	core.providers = nil
	core.bindings = nil
	core.consumed = true
	core.mu.Unlock()

	entries, err := materializeOwners(definitions, graph)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{core: &runtimeCore{
		entries:   entries,
		frontiers: graph.frontiers,
		cleanup:   make([]bool, len(entries)),
		state:     runtimeIdle,
	}}
	return runtime, nil
}

func snapshotOwners(core *registryCore) []ownerDefinition {
	definitions := make(
		[]ownerDefinition,
		0,
		len(core.declarations)+len(core.providers),
	)
	for _, declared := range core.declarations {
		definitions = append(definitions, ownerDefinition{
			node:         declared.node,
			dependencies: append([]nodeDescriptor(nil), declared.dependencies...),
			lifecycle:    declared.lifecycle,
			value:        declared.value,
		})
	}
	for _, registered := range core.providers {
		provider := providerEntry{
			node:         registered.node,
			constructor:  registered.constructor,
			parameters:   append([]reflect.Type(nil), registered.parameters...),
			returnsError: registered.returnsError,
			orderAfter:   append([]nodeDescriptor(nil), registered.orderAfter...),
		}
		definitions = append(definitions, ownerDefinition{
			node:         provider.node,
			dependencies: append([]nodeDescriptor(nil), provider.orderAfter...),
			provider:     provider,
			provided:     true,
		})
	}
	sort.Slice(definitions, func(i, j int) bool {
		return descriptorLess(definitions[i].node, definitions[j].node)
	})
	return definitions
}

func compileOwners(
	definitions []ownerDefinition,
	bindings map[reflect.Type]*nodeIdentity,
) (compiledGraph, error) {
	indices := make(map[*nodeIdentity]int, len(definitions))
	exact := make(map[reflect.Type][]int, len(definitions))
	for index := range definitions {
		definition := &definitions[index]
		indices[definition.node.identity] = index
		exact[definition.node.declaredType] = append(
			exact[definition.node.declaredType],
			index,
		)
	}

	for boundType, identity := range bindings {
		index, exists := indices[identity]
		if !exists {
			return compiledGraph{}, fmt.Errorf(
				"%w: binding for %s selects an unregistered owner",
				ErrInvalidBinding,
				boundType,
			)
		}
		if !definitions[index].node.declaredType.AssignableTo(boundType) {
			return compiledGraph{}, fmt.Errorf(
				"%w: owner %s is not assignable to %s",
				ErrInvalidBinding,
				describeOwner(definitions[index]),
				boundType,
			)
		}
	}

	for index := range definitions {
		definition := &definitions[index]
		if !definition.provided {
			continue
		}

		explicit := make(map[*nodeIdentity]struct{}, len(definition.dependencies))
		for _, dependency := range definition.dependencies {
			explicit[dependency.identity] = struct{}{}
		}
		automatic := make(map[*nodeIdentity]struct{}, len(definition.provider.parameters))
		definition.arguments = make([]int, len(definition.provider.parameters))
		for parameterIndex, parameterType := range definition.provider.parameters {
			dependencyIndex, err := resolveParameter(
				*definition,
				parameterIndex,
				parameterType,
				definitions,
				indices,
				exact,
				bindings,
			)
			if err != nil {
				return compiledGraph{}, err
			}
			definition.arguments[parameterIndex] = dependencyIndex
			dependency := definitions[dependencyIndex].node
			if _, exists := explicit[dependency.identity]; exists {
				return compiledGraph{}, fmt.Errorf(
					"%w: provider %q receives %s as parameter %d and also names it in orderAfter",
					ErrDuplicateDependency,
					definition.node.label,
					parameterType,
					parameterIndex,
				)
			}
			if _, exists := automatic[dependency.identity]; exists {
				continue
			}
			automatic[dependency.identity] = struct{}{}
			definition.dependencies = append(
				definition.dependencies,
				dependency,
			)
		}
	}

	graphDefinitions := make([]graphDefinition, len(definitions))
	for index := range definitions {
		graphDefinitions[index] = graphDefinition{
			node: definitions[index].node,
			dependencies: append(
				[]nodeDescriptor(nil),
				definitions[index].dependencies...,
			),
		}
	}
	return compileGraph(graphDefinitions)
}

func resolveParameter(
	provider ownerDefinition,
	parameterIndex int,
	parameterType reflect.Type,
	definitions []ownerDefinition,
	indices map[*nodeIdentity]int,
	exact map[reflect.Type][]int,
	bindings map[reflect.Type]*nodeIdentity,
) (int, error) {
	if identity, exists := bindings[parameterType]; exists {
		return indices[identity], nil
	}

	candidates := exact[parameterType]
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return 0, ambiguousParameterError(
			provider,
			parameterIndex,
			parameterType,
			candidates,
			definitions,
		)
	}

	if parameterType.Kind() == reflect.Interface {
		candidates = make([]int, 0, len(definitions))
		for index := range definitions {
			if definitions[index].node.declaredType.AssignableTo(parameterType) {
				candidates = append(candidates, index)
			}
		}
		if len(candidates) == 1 {
			return candidates[0], nil
		}
		if len(candidates) > 1 {
			return 0, ambiguousParameterError(
				provider,
				parameterIndex,
				parameterType,
				candidates,
				definitions,
			)
		}
	}

	return 0, fmt.Errorf(
		"%w: provider %q parameter %d %s has no registered owner",
		ErrNotRegistered,
		provider.node.label,
		parameterIndex,
		parameterType,
	)
}

func ambiguousParameterError(
	provider ownerDefinition,
	parameterIndex int,
	parameterType reflect.Type,
	candidates []int,
	definitions []ownerDefinition,
) error {
	descriptions := make([]string, len(candidates))
	for index, candidate := range candidates {
		descriptions[index] = describeOwner(definitions[candidate])
	}
	return fmt.Errorf(
		"%w: provider %q parameter %d %s matches %s",
		ErrAmbiguousDependency,
		provider.node.label,
		parameterIndex,
		parameterType,
		strings.Join(descriptions, ", "),
	)
}

func describeOwner(definition ownerDefinition) string {
	label := definition.node.label
	if label == "" {
		label = "<unnamed>"
	}
	return fmt.Sprintf(
		"%q (%s, ordinal %d)",
		label,
		definition.node.declaredType,
		definition.node.ordinal,
	)
}

func compileGraph(definitions []graphDefinition) (compiledGraph, error) {
	entries := make([]graphEntry, len(definitions))
	indices := make(map[*nodeIdentity]int, len(definitions))
	for index, definition := range definitions {
		indices[definition.node.identity] = index
		entries[index] = graphEntry{node: definition.node}
	}

	for index := range definitions {
		definition := &definitions[index]
		dependencyDescriptors := append(
			[]nodeDescriptor(nil),
			definition.dependencies...,
		)
		sort.Slice(dependencyDescriptors, func(i, j int) bool {
			return descriptorLess(dependencyDescriptors[i], dependencyDescriptors[j])
		})
		dependencies := make([]int, 0, len(dependencyDescriptors))
		for _, dependency := range dependencyDescriptors {
			dependencyIndex, exists := indices[dependency.identity]
			if !exists {
				return compiledGraph{}, fmt.Errorf(
					"%w: node %q depends on %q",
					ErrNotRegistered,
					definition.node.label,
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
		return compiledGraph{}, fmt.Errorf("%w: %s", ErrCyclicDependency, witness)
	}
	return compiledGraph{entries: entries, frontiers: frontiers}, nil
}

func materializeOwners(
	definitions []ownerDefinition,
	graph compiledGraph,
) ([]runtimeEntry, error) {
	values := make([]reflect.Value, len(definitions))
	lifecycles := make([]Lifecycle, len(definitions))
	for index := range definitions {
		if definitions[index].provided {
			continue
		}
		values[index] = definitions[index].value
		lifecycles[index] = definitions[index].lifecycle
	}

	for _, frontier := range graph.frontiers {
		for _, index := range frontier {
			definition := definitions[index]
			if !definition.provided {
				continue
			}
			arguments := make([]reflect.Value, len(definition.arguments))
			for argumentIndex, dependencyIndex := range definition.arguments {
				arguments[argumentIndex] = values[dependencyIndex]
			}
			value, lifecycle, err := invokeProvider(definition.provider, arguments)
			if err != nil {
				return nil, err
			}
			values[index] = value
			lifecycles[index] = lifecycle
		}
	}

	entries := make([]runtimeEntry, len(graph.entries))
	for index := range graph.entries {
		entries[index] = runtimeEntry{
			graphEntry: graph.entries[index],
			lifecycle:  lifecycles[index],
		}
	}
	return entries, nil
}

type providerResult struct {
	value     reflect.Value
	lifecycle Lifecycle
	err       error
}

type providerConstructionError struct {
	message string
	cause   error
	stack   string
}

func (err *providerConstructionError) Error() string {
	if err.stack == "" {
		return err.message
	}
	return err.message + "\n" + err.stack
}

func (err *providerConstructionError) Unwrap() []error {
	if err.cause == nil {
		return []error{ErrConstruction}
	}
	return []error{ErrConstruction, err.cause}
}

func invokeProvider(
	provider providerEntry,
	arguments []reflect.Value,
) (reflect.Value, Lifecycle, error) {
	results := make(chan providerResult, 1)
	go callProvider(provider, arguments, results)
	result := <-results
	return result.value, result.lifecycle, result.err
}

func callProvider(
	provider providerEntry,
	arguments []reflect.Value,
	results chan<- providerResult,
) {
	result := providerResult{}
	completed := false
	defer func() {
		if recovered := recover(); recovered != nil {
			result = providerResult{err: providerPanicError(provider, recovered)}
		} else if !completed {
			result = providerResult{err: newProviderConstructionError(
				provider,
				"aborted with runtime.Goexit",
				nil,
				nil,
			)}
		}
		results <- result
	}()

	outputs := provider.constructor.Call(arguments)
	if provider.returnsError && !outputs[1].IsNil() {
		cause := outputs[1].Interface().(error)
		result.err = newProviderConstructionError(
			provider,
			"returned an error of type "+dynamicTypeName(cause),
			cause,
			nil,
		)
		completed = true
		return
	}

	value := outputs[0]
	lifecycle, ok := value.Interface().(Lifecycle)
	if !ok || isNilLifecycle(lifecycle) {
		result.err = newProviderConstructionError(
			provider,
			"returned a nil lifecycle",
			ErrInvalidLifecycle,
			nil,
		)
		completed = true
		return
	}
	result = providerResult{value: value, lifecycle: lifecycle}
	completed = true
}

func providerPanicError(provider providerEntry, recovered any) error {
	if cause, ok := recovered.(error); ok {
		return newProviderConstructionError(
			provider,
			"panicked with "+dynamicTypeName(recovered),
			cause,
			debug.Stack(),
		)
	}
	return newProviderConstructionError(
		provider,
		"panicked with "+dynamicTypeName(recovered),
		nil,
		debug.Stack(),
	)
}

func newProviderConstructionError(
	provider providerEntry,
	detail string,
	cause error,
	stack []byte,
) error {
	return &providerConstructionError{
		message: fmt.Sprintf(
			"%s: provider %q (%s) %s",
			ErrConstruction.Error(),
			provider.node.label,
			provider.node.declaredType.String(),
			detail,
		),
		cause: cause,
		stack: string(stack),
	}
}

func dynamicTypeName(value any) string {
	typeOf := reflect.TypeOf(value)
	if typeOf == nil {
		return "<nil>"
	}
	return typeOf.String()
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

func kahnFrontiers(entries []graphEntry) ([][]int, []bool) {
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

func cycleWitness(entries []graphEntry, residual []bool) string {
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

func formatCycle(entries []graphEntry, cycle []int) string {
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
