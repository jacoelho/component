package component

import (
	"fmt"
	"reflect"
)

var (
	lifecycleType = reflect.TypeFor[Lifecycle]()
	errorType     = reflect.TypeFor[error]()
)

type providerEntry struct {
	node         nodeDescriptor
	constructor  reflect.Value
	parameters   []reflect.Type
	returnsError bool
	orderAfter   []nodeDescriptor
}

type providedNode struct {
	identity *nodeIdentity
}

func (n *providedNode) String() string {
	if n == nil || n.identity == nil {
		return "<invalid>"
	}
	return n.identity.label
}

func (n *providedNode) nodeIdentity() *nodeIdentity {
	if n == nil {
		return nil
	}
	return n.identity
}

// Provide declares a lifecycle constructor. Compile resolves its parameters
// from registered lifecycle owners, validates the complete graph, and invokes
// constructors in dependency order.
//
// A constructor must be a non-variadic func(D1, ..., Dn) T or
// func(D1, ..., Dn) (T, error), where T implements Lifecycle. Constructors
// must be inert: acquire resources in Configure or Start, not here. Injected
// owners are borrowed; a dependent must stop only the resources it creates.
func (r *Registry) Provide(
	label string,
	constructor any,
	orderAfter ...NodeRef,
) (NodeRef, error) {
	if r == nil {
		return nil, fmt.Errorf("component: provide on nil registry")
	}

	core := r.ensureCore()
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.consumed {
		return nil, ErrRegistryConsumed
	}

	entry, err := inspectConstructor(label, constructor)
	if err != nil {
		return nil, err
	}

	dependencies := make([]nodeDescriptor, 0, len(orderAfter))
	seen := make(map[*nodeIdentity]struct{}, len(orderAfter))
	for _, dependency := range orderAfter {
		identity := nodeRefIdentity(dependency)
		if identity == nil {
			return nil, fmt.Errorf(
				"%w: order-only dependency of provider %q",
				ErrInvalidNode,
				label,
			)
		}
		if _, exists := seen[identity]; exists {
			return nil, fmt.Errorf(
				"%w: provider %q depends on %q more than once",
				ErrDuplicateDependency,
				label,
				dependency.String(),
			)
		}
		seen[identity] = struct{}{}
		dependencies = append(dependencies, descriptor(dependency))
	}

	identity := newNodeIdentity(label, entry.node.declaredType)
	entry.node = nodeDescriptor{
		identity:     identity,
		label:        identity.label,
		ordinal:      identity.ordinal,
		declaredType: identity.declaredType,
	}
	entry.orderAfter = dependencies
	if core.providers == nil {
		core.providers = make(map[*nodeIdentity]providerEntry)
	}
	core.providers[identity] = entry
	return &providedNode{identity: identity}, nil
}

func inspectConstructor(label string, constructor any) (providerEntry, error) {
	value := reflect.ValueOf(constructor)
	if !value.IsValid() || value.Kind() != reflect.Func || value.IsNil() {
		return providerEntry{}, fmt.Errorf(
			"%w: provider %q requires a non-nil function",
			ErrInvalidConstructor,
			label,
		)
	}

	typeOf := value.Type()
	if typeOf.IsVariadic() {
		return providerEntry{}, fmt.Errorf(
			"%w: provider %q constructor %s is variadic",
			ErrInvalidConstructor,
			label,
			typeOf,
		)
	}
	if typeOf.NumOut() != 1 && typeOf.NumOut() != 2 {
		return providerEntry{}, fmt.Errorf(
			"%w: provider %q constructor %s must return T or (T, error)",
			ErrInvalidConstructor,
			label,
			typeOf,
		)
	}
	if typeOf.NumOut() == 2 && typeOf.Out(1) != errorType {
		return providerEntry{}, fmt.Errorf(
			"%w: provider %q constructor %s second result must be error",
			ErrInvalidConstructor,
			label,
			typeOf,
		)
	}
	resultType := typeOf.Out(0)
	if !resultType.Implements(lifecycleType) {
		return providerEntry{}, fmt.Errorf(
			"%w: provider %q result %s does not implement Lifecycle",
			ErrInvalidConstructor,
			label,
			resultType,
		)
	}

	parameters := make([]reflect.Type, typeOf.NumIn())
	for index := range parameters {
		parameters[index] = typeOf.In(index)
	}
	return providerEntry{
		node:         nodeDescriptor{declaredType: resultType},
		constructor:  value,
		parameters:   parameters,
		returnsError: typeOf.NumOut() == 2,
	}, nil
}

// Bind selects owner whenever a provider constructor requests T. It creates
// no lifecycle owner, transfers no ownership, and does not prevent other
// registered owners from running.
func (r *Registry) Bind[T any](owner NodeRef) error {
	if r == nil {
		return fmt.Errorf("component: bind on nil registry")
	}

	core := r.ensureCore()
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.consumed {
		return ErrRegistryConsumed
	}

	identity := nodeRefIdentity(owner)
	if identity == nil {
		return fmt.Errorf("%w: binding owner", ErrInvalidNode)
	}

	if _, registered := core.declarations[identity]; !registered {
		if _, provided := core.providers[identity]; !provided {
			return fmt.Errorf("%w: binding owner %q", ErrNotRegistered, owner.String())
		}
	}

	boundType := reflect.TypeFor[T]()
	if !identity.declaredType.AssignableTo(boundType) {
		return fmt.Errorf(
			"%w: owner %q declared as %s is not assignable to %s",
			ErrInvalidBinding,
			owner.String(),
			identity.declaredType,
			boundType,
		)
	}
	if _, exists := core.bindings[boundType]; exists {
		return fmt.Errorf("%w: type %s is already bound", ErrInvalidBinding, boundType)
	}
	if core.bindings == nil {
		core.bindings = make(map[reflect.Type]*nodeIdentity)
	}
	core.bindings[boundType] = identity
	return nil
}
