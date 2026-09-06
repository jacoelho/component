package component

import (
	"context"
	"fmt"
	"reflect"
)

// Value supplies an already-created borrowed value. A function value is
// stored as a value and is never invoked by the component package.
func Value[T any](value T) Ref[T] {
	return Ref[T]{node: &definition{
		declaredType: reflect.TypeFor[T](),
		create: func(context.Context, []any) (any, error) {
			return &valueCell[T]{value: value}, nil
		},
	}}
}

// Provide declares a no-error, context-free factory.
func Provide[T any](create func() T, ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("Provide requires a non-nil constructor")
	}
	return newDefinition(nil, func(context.Context, []any) (T, error) {
		return create(), nil
	}, ownership...)
}

// TryProvide declares a context-free factory that may fail.
func TryProvide[T any](create func() (T, error), ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("TryProvide requires a non-nil constructor")
	}
	return newDefinition(nil, func(context.Context, []any) (T, error) {
		return create()
	}, ownership...)
}

// ProvideContext declares a factory that receives the caller's startup
// context and may fail.
func ProvideContext[T any](create func(context.Context) (T, error), ownership ...Ownership[T]) Ref[T] {
	if create == nil {
		return invalidDefinition[T]("ProvideContext requires a non-nil constructor")
	}
	return newDefinition(nil, func(ctx context.Context, _ []any) (T, error) {
		return create(ctx)
	}, ownership...)
}

type typedFactory[T any] func(context.Context, []any) (T, error)

func newDefinition[T any](inputs []*definition, factory typedFactory[T], ownership ...Ownership[T]) Ref[T] {
	d := &definition{
		inputs:       append([]*definition(nil), inputs...),
		declaredType: reflect.TypeFor[T](),
	}

	if len(ownership) > 1 {
		d.err = invalidDefinitionError("at most one ownership declaration is allowed")
		return Ref[T]{node: d}
	}

	owned := len(ownership) == 1
	if owned {
		configured := ownership[0]
		d.name = configured.Name
		if configured.bind == nil {
			d.err = invalidDefinitionError("ownership must be declared with Managed")
			return Ref[T]{node: d}
		}
		d.start = func(ctx context.Context, boxed any) error {
			return configured.bind(boxed.(*valueCell[T]).value).Start(ctx)
		}
		d.stop = func(ctx context.Context, boxed any) error {
			return configured.bind(boxed.(*valueCell[T]).value).Stop(ctx)
		}
	}

	d.create = func(ctx context.Context, args []any) (any, error) {
		value, err := factory(ctx, args)
		if err != nil {
			return nil, err
		}
		if owned && isNilValue(value) {
			return nil, fmt.Errorf("%w: managed factory returned nil %s", ErrInvalidValue, d.declaredType)
		}
		return &valueCell[T]{value: value}, nil
	}
	return Ref[T]{node: d}
}

func invalidDefinition[T any](reason string) Ref[T] {
	return Ref[T]{node: &definition{
		declaredType: reflect.TypeFor[T](),
		err:          invalidDefinitionError(reason),
	}}
}

func invalidDefinitionError(reason string) error {
	return fmt.Errorf("%w: %s", ErrInvalidDefinition, reason)
}

func isNilValue[T any](value T) bool {
	boxed := reflect.ValueOf(value)
	if !boxed.IsValid() {
		return true
	}
	switch boxed.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice, reflect.UnsafePointer:
		return boxed.IsNil()
	default:
		return false
	}
}

func inputValue[T any](args []any, index int) T {
	return args[index].(*valueCell[T]).value
}
