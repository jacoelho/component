package component

import (
	"context"
	"fmt"
	"reflect"
)

// Lifecycle defines the temporal boundaries of a component's operation.
// Implementations should ensure Stop is idempotent and cleans up after Start.
type Lifecycle interface {
	// Start initializes long-lived resources.
	// It is called after dependencies have started and should return when
	// initialization is complete.
	Start(context.Context) error

	// Stop releases resources and terminates operations.
	// It is called in reverse dependency order and should return when cleanup
	// is complete.
	Stop(context.Context) error
}

// Constructor creates a component instance for a runtime.
// Use Get inside constructors to retrieve already-started dependencies.
type Constructor[T Lifecycle] func(*Runtime) (T, error)

// Key identifies a component producing type T in the registry.
type Key[T Lifecycle] struct {
	name string
}

// NewKey returns a Key[T] with the given name.
// Used for disambiguating multiple instances of the same type T.
// If types are different, names can be the same or empty without collision.
func NewKey[T Lifecycle](name string) Key[T] {
	return Key[T]{name: name}
}

// id returns the unique string identifier for this key.
func (k Key[T]) id() string {
	typ := reflect.TypeFor[T]().String()
	if k.name != "" {
		return fmt.Sprintf("%s(%s)", typ, k.name)
	}
	return typ
}

func (k Key[T]) String() string {
	return k.id()
}

// Keyer provides type erasure for heterogeneous component keys.
type Keyer interface {
	id() string
}
