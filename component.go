package component

import (
	"context"
	"fmt"
	"reflect"
)

// Lifecycle defines the temporal boundaries of a component's operation.
// Implementations should ensure Stop is idempotent and cleans up after Start.
type Lifecycle interface {
	// Start initializes long-lived resources. Called in dependency order.
	Start(context.Context) error
	// Stop releases resources and terminates operations.
	// Called in reverse dependency order during shutdown.
	Stop(context.Context) error
}

// Key uniquely identifies a component producing type T in the system.
type Key[T Lifecycle] struct {
	name string
}

// NewKey returns a Key[T] with the given name.
// name must be non‐empty if you plan to have >1 Key[T] in your System.
// Repeated names with different underlying T is allowed.
func NewKey[T Lifecycle](name string) Key[T] {
	return Key[T]{name: name}
}

// id returns the unique string for this key.
func (k Key[T]) id() string {
	var zero T
	typ := reflect.TypeOf(zero).String()

	if k.name != "" {
		return fmt.Sprintf("%s(%s)", typ, k.name)
	}

	return typ
}

// typ returns reflect.Type of T (used for type‐assertion on Get).
func (Key[T]) typ() reflect.Type {
	var zero T
	return reflect.TypeOf(zero)
}

// String reports the type.
func (k Key[T]) String() string {
	return fmt.Sprintf("%v", k.typ())
}

// keyer provides type erasure for heterogeneous component keys.
type keyer interface {
	id() string
	typ() reflect.Type
}
