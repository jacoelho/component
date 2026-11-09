package component

import "context"

//go:generate go run ./internal/genmap

// ProvideValue declares a node built by fn, which takes no inputs and cannot
// fail.
func ProvideValue[R any](fn func() R, opts ...Option[R]) Ref[R] {
	if fn == nil {
		return invalid("ProvideValue", nil, opts)
	}
	return declare(funcLabel(fn), nil, func(context.Context, []any) (R, error) { return fn(), nil }, opts)
}

// Provide declares a node built by fn, which takes no inputs and may fail.
func Provide[R any](fn func() (R, error), opts ...Option[R]) Ref[R] {
	if fn == nil {
		return invalid("Provide", nil, opts)
	}
	return declare(funcLabel(fn), nil, func(context.Context, []any) (R, error) { return fn() }, opts)
}

// ProvideContext declares a node built by fn, which receives the context
// given to New and may fail.
func ProvideContext[R any](fn func(context.Context) (R, error), opts ...Option[R]) Ref[R] {
	if fn == nil {
		return invalid("ProvideContext", nil, opts)
	}
	return declare(funcLabel(fn), nil, func(ctx context.Context, _ []any) (R, error) { return fn(ctx) }, opts)
}
