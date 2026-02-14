package component

import "slices"

type componentSpec struct {
	constructor  func(*Runtime) (any, error)
	dependencies []string
}

func cloneSpecs(entries map[string]*componentSpec) map[string]*componentSpec {
	if len(entries) == 0 {
		return make(map[string]*componentSpec)
	}

	cloned := make(map[string]*componentSpec, len(entries))
	for id, spec := range entries {
		if spec == nil {
			cloned[id] = nil
			continue
		}
		cloned[id] = &componentSpec{
			constructor:  spec.constructor,
			dependencies: slices.Clone(spec.dependencies),
		}
	}

	return cloned
}

func dependencyMap(entries map[string]*componentSpec) map[string][]string {
	deps := make(map[string][]string, len(entries))
	for id, spec := range entries {
		if spec == nil {
			deps[id] = nil
			continue
		}
		deps[id] = slices.Clone(spec.dependencies)
	}
	return deps
}
