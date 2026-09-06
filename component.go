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

// Inputs2 stores two typed references in their caller-specified order.
type Inputs2[A, B any] struct {
	a Ref[A]
	b Ref[B]
}

func (in Inputs2[A, B]) refs() []*definition {
	return []*definition{in.a.node, in.b.node}
}

func (in Inputs2[A, B]) Map[T any](create func(A, B) T, ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs2.Map requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(_ context.Context, args []any) (T, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1)), nil
	}, ownership...)
}

func (in Inputs2[A, B]) TryMap[T any](create func(A, B) (T, error), ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs2.TryMap requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(_ context.Context, args []any) (T, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1))
	}, ownership...)
}

func (in Inputs2[A, B]) MapContext[T any](create func(context.Context, A, B) (T, error), ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs2.MapContext requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(ctx context.Context, args []any) (T, error) {
		return create(ctx, inputValue[A](args, 0), inputValue[B](args, 1))
	}, ownership...)
}

func (in Inputs2[A, B]) With[C any](other Ref[C]) Inputs3[A, B, C] {
	return Inputs3[A, B, C]{a: in.a, b: in.b, c: other}
}

// Inputs3 stores three typed references in their caller-specified order.
type Inputs3[A, B, C any] struct {
	a Ref[A]
	b Ref[B]
	c Ref[C]
}

func (in Inputs3[A, B, C]) refs() []*definition {
	return []*definition{in.a.node, in.b.node, in.c.node}
}

func (in Inputs3[A, B, C]) Map[T any](create func(A, B, C) T, ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs3.Map requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(_ context.Context, args []any) (T, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2)), nil
	}, ownership...)
}

func (in Inputs3[A, B, C]) TryMap[T any](create func(A, B, C) (T, error), ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs3.TryMap requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(_ context.Context, args []any) (T, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2))
	}, ownership...)
}

func (in Inputs3[A, B, C]) MapContext[T any](create func(context.Context, A, B, C) (T, error), ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs3.MapContext requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(ctx context.Context, args []any) (T, error) {
		return create(ctx, inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2))
	}, ownership...)
}

func (in Inputs3[A, B, C]) With[D any](other Ref[D]) Inputs4[A, B, C, D] {
	return Inputs4[A, B, C, D]{a: in.a, b: in.b, c: in.c, d: other}
}

// Inputs4 stores four typed references in their caller-specified order.
type Inputs4[A, B, C, D any] struct {
	a Ref[A]
	b Ref[B]
	c Ref[C]
	d Ref[D]
}

func (in Inputs4[A, B, C, D]) refs() []*definition {
	return []*definition{in.a.node, in.b.node, in.c.node, in.d.node}
}

func (in Inputs4[A, B, C, D]) Map[T any](create func(A, B, C, D) T, ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs4.Map requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(_ context.Context, args []any) (T, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2), inputValue[D](args, 3)), nil
	}, ownership...)
}

func (in Inputs4[A, B, C, D]) TryMap[T any](create func(A, B, C, D) (T, error), ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs4.TryMap requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(_ context.Context, args []any) (T, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2), inputValue[D](args, 3))
	}, ownership...)
}

func (in Inputs4[A, B, C, D]) MapContext[T any](create func(context.Context, A, B, C, D) (T, error), ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Inputs4.MapContext requires a non-nil constructor")
	}
	return newDefinition(in.refs(), func(ctx context.Context, args []any) (T, error) {
		return create(ctx, inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2), inputValue[D](args, 3))
	}, ownership...)
}

// Map creates one typed dependent from one typed input.
func (r Ref[A]) Map[B any](create func(A) B, ownership ...Ownership[B]) Ref[B] {
	if create == nil {
		return invalidDefinition[B]("Ref.Map requires a non-nil constructor")
	}
	return newDefinition([]*definition{r.node}, func(_ context.Context, args []any) (B, error) {
		return create(inputValue[A](args, 0)), nil
	}, ownership...)
}

// TryMap creates one typed dependent from one typed input and propagates its
// constructor error without invoking lifecycle hooks.
func (r Ref[A]) TryMap[B any](create func(A) (B, error), ownership ...Ownership[B]) Ref[B] {
	if create == nil {
		return invalidDefinition[B]("Ref.TryMap requires a non-nil constructor")
	}
	return newDefinition([]*definition{r.node}, func(_ context.Context, args []any) (B, error) {
		return create(inputValue[A](args, 0))
	}, ownership...)
}

// MapContext creates one typed dependent from one typed input and the caller's
// startup context.
func (r Ref[A]) MapContext[B any](create func(context.Context, A) (B, error), ownership ...Ownership[B]) Ref[B] {
	if create == nil {
		return invalidDefinition[B]("Ref.MapContext requires a non-nil constructor")
	}
	return newDefinition([]*definition{r.node}, func(ctx context.Context, args []any) (B, error) {
		return create(ctx, inputValue[A](args, 0))
	}, ownership...)
}

// With groups a second typed input without creating a definition.
func (r Ref[A]) With[B any](other Ref[B]) Inputs2[A, B] {
	return Inputs2[A, B]{a: r, b: other}
}
