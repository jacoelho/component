package component

import (
	"context"
	"reflect"
)

// Root is a value that can be used as a runtime root. The unexported method
// keeps the reference boundary closed to application-defined values.
type Root interface {
	definition() *definition
}

// Ref describes one typed definition in a construction graph. It carries no
// runtime instance; a Runtime creates one instance for each reachable Ref.
//
// The named valueCell marker prevents conversions between different T,
// including structs differing only in tags. Its zero-length pointer array
// keeps every Ref comparable, even when T is a slice, map, or function.
type Ref[T any] struct {
	_    [0]*valueCell[T]
	node *definition
}

func (r Ref[T]) definition() *definition {
	return r.node
}

// valueCell preserves the declared T, including a nil interface value, while
// allowing the runtime to store cells behind an erased definition boundary.
type valueCell[T any] struct {
	value T
}

// definition is the immutable construction description shared by Ref values.
// The runtime owns instances of definitions; this type owns only typed
// adapters and lifecycle-hook translation.
type definition struct {
	inputs       []*definition
	name         string
	declaredType reflect.Type
	err          error
	create       func(context.Context, []any) (any, error)
	start        func(context.Context, any) error
	stop         func(context.Context, any) error
}

// Lifecycle is the contract of a managed resource owner. Start returns when the
// owner is ready. Stop returns nil only when owned work has finished and cannot
// use dependencies again. Stop must tolerate retries after partial failure.
type Lifecycle interface {
	Start(context.Context) error
	Stop(context.Context) error
}

// Ownership opts a factory into lifecycle management. Obtain it from Managed;
// its zero value is invalid. Name is an optional diagnostic label.
type Ownership[T any] struct {
	Name string
	bind func(T) Lifecycle
}

// Managed declares ownership of a factory result whose methods implement
// Lifecycle. Merely implementing Lifecycle never enables management implicitly.
func Managed[T Lifecycle]() Ownership[T] {
	return Ownership[T]{bind: func(value T) Lifecycle { return value }}
}
