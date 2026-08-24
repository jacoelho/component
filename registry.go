package component

import (
	"fmt"
	"reflect"
	"sync"
)

// Registry collects lifecycle declarations. The zero Registry is usable.
// Copies made after first use share the same registry identity. Compile
// consumes that identity after structural validation succeeds.
type Registry struct {
	core *registryCore
}

type registryCore struct {
	declarations map[*nodeIdentity]declaration
	providers    map[*nodeIdentity]providerEntry
	bindings     map[reflect.Type]*nodeIdentity
	mu           sync.Mutex
	consumed     bool
}

type declaration struct {
	node         nodeDescriptor
	lifecycle    Lifecycle
	value        reflect.Value
	dependencies []nodeDescriptor
}

var registryInitializationMu sync.RWMutex

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{core: &registryCore{}}
}

// Register declares an already constructed lifecycle owned by node and its
// lifecycle-ordering dependencies. Dependencies carry no values: they only
// require that each dependency starts before node and stops after node. The
// declared T may satisfy parameters of constructors registered with Provide.
func (r *Registry) Register[T Lifecycle](
	node *Node[T],
	lifecycle T,
	dependencies ...NodeRef,
) error {
	if r == nil {
		return fmt.Errorf("component: register on nil registry")
	}

	core := r.ensureCore()
	core.mu.Lock()
	defer core.mu.Unlock()

	if core.consumed {
		return ErrRegistryConsumed
	}
	identity := node.nodeIdentity()
	if identity == nil {
		return fmt.Errorf("%w: registration owner", ErrInvalidNode)
	}
	if isNilLifecycle(lifecycle) {
		return fmt.Errorf("%w: node %q", ErrInvalidLifecycle, node.String())
	}
	if _, exists := core.declarations[identity]; exists {
		return fmt.Errorf("%w: node %q", ErrAlreadyRegistered, node.String())
	}

	dependencyDescriptors := make([]nodeDescriptor, 0, len(dependencies))
	seen := make(map[*nodeIdentity]struct{}, len(dependencies))
	for _, dependency := range dependencies {
		dependencyIdentity := nodeRefIdentity(dependency)
		if dependencyIdentity == nil {
			return fmt.Errorf("%w: dependency of node %q", ErrInvalidNode, node.String())
		}
		if _, exists := seen[dependencyIdentity]; exists {
			return fmt.Errorf(
				"%w: node %q depends on %q more than once",
				ErrDuplicateDependency,
				node.String(),
				dependency.String(),
			)
		}
		seen[dependencyIdentity] = struct{}{}
		dependencyDescriptors = append(dependencyDescriptors, descriptor(dependency))
	}

	if core.declarations == nil {
		core.declarations = make(map[*nodeIdentity]declaration)
	}
	core.declarations[identity] = declaration{
		node:         descriptor(node),
		lifecycle:    lifecycle,
		value:        declaredValue(identity.declaredType, lifecycle),
		dependencies: dependencyDescriptors,
	}
	return nil
}

func declaredValue(declaredType reflect.Type, value any) reflect.Value {
	boxed := reflect.New(declaredType).Elem()
	boxed.Set(reflect.ValueOf(value))
	return boxed
}

func nodeRefIdentity(node NodeRef) *nodeIdentity {
	if node == nil {
		return nil
	}
	return node.nodeIdentity()
}

func (r *Registry) ensureCore() *registryCore {
	registryInitializationMu.RLock()
	core := r.core
	registryInitializationMu.RUnlock()
	if core != nil {
		return core
	}

	registryInitializationMu.Lock()
	defer registryInitializationMu.Unlock()
	if r.core == nil {
		r.core = &registryCore{}
	}
	return r.core
}

func isNilLifecycle(lifecycle Lifecycle) bool {
	if lifecycle == nil {
		return true
	}
	value := reflect.ValueOf(lifecycle)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
