package component

import "context"

// MapValue declares a constructor without an error result from one typed input.
func MapValue[R, A any](input Ref[A], create func(A) R, ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("MapValue requires a non-nil constructor")
	}
	return newDefinition([]*definition{input.node}, func(_ context.Context, args []any) (R, error) {
		return create(inputValue[A](args, 0)), nil
	}, ownership...)
}

// Map declares a fallible constructor from one typed input.
func Map[R, A any](input Ref[A], create func(A) (R, error), ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("Map requires a non-nil constructor")
	}
	return newDefinition([]*definition{input.node}, func(_ context.Context, args []any) (R, error) {
		return create(inputValue[A](args, 0))
	}, ownership...)
}

// MapContext declares a fallible constructor from one typed input.
// The constructor receives the caller's startup context.
func MapContext[R, A any](input Ref[A], create func(context.Context, A) (R, error), ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("MapContext requires a non-nil constructor")
	}
	return newDefinition([]*definition{input.node}, func(ctx context.Context, args []any) (R, error) {
		return create(ctx, inputValue[A](args, 0))
	}, ownership...)
}

// MapValue2 declares a constructor without an error result from two typed inputs.
func MapValue2[R, A, B any](a Ref[A], b Ref[B], create func(A, B) R, ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("MapValue2 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node}, func(_ context.Context, args []any) (R, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1)), nil
	}, ownership...)
}

// Map2 declares a fallible constructor from two typed inputs.
func Map2[R, A, B any](a Ref[A], b Ref[B], create func(A, B) (R, error), ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("Map2 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node}, func(_ context.Context, args []any) (R, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1))
	}, ownership...)
}

// MapContext2 declares a fallible constructor from two typed inputs.
// The constructor receives the caller's startup context.
func MapContext2[R, A, B any](a Ref[A], b Ref[B], create func(context.Context, A, B) (R, error), ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("MapContext2 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node}, func(ctx context.Context, args []any) (R, error) {
		return create(ctx, inputValue[A](args, 0), inputValue[B](args, 1))
	}, ownership...)
}

// MapValue3 declares a constructor without an error result from three typed inputs.
func MapValue3[R, A, B, C any](a Ref[A], b Ref[B], c Ref[C], create func(A, B, C) R, ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("MapValue3 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node, c.node}, func(_ context.Context, args []any) (R, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2)), nil
	}, ownership...)
}

// Map3 declares a fallible constructor from three typed inputs.
func Map3[R, A, B, C any](a Ref[A], b Ref[B], c Ref[C], create func(A, B, C) (R, error), ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("Map3 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node, c.node}, func(_ context.Context, args []any) (R, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2))
	}, ownership...)
}

// MapContext3 declares a fallible constructor from three typed inputs.
// The constructor receives the caller's startup context.
func MapContext3[R, A, B, C any](a Ref[A], b Ref[B], c Ref[C], create func(context.Context, A, B, C) (R, error), ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("MapContext3 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node, c.node}, func(ctx context.Context, args []any) (R, error) {
		return create(ctx, inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2))
	}, ownership...)
}

// MapValue4 declares a constructor without an error result from four typed inputs.
func MapValue4[R, A, B, C, D any](a Ref[A], b Ref[B], c Ref[C], d Ref[D], create func(A, B, C, D) R, ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("MapValue4 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node, c.node, d.node}, func(_ context.Context, args []any) (R, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2), inputValue[D](args, 3)), nil
	}, ownership...)
}

// Map4 declares a fallible constructor from four typed inputs.
func Map4[R, A, B, C, D any](a Ref[A], b Ref[B], c Ref[C], d Ref[D], create func(A, B, C, D) (R, error), ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("Map4 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node, c.node, d.node}, func(_ context.Context, args []any) (R, error) {
		return create(inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2), inputValue[D](args, 3))
	}, ownership...)
}

// MapContext4 declares a fallible constructor from four typed inputs.
// The constructor receives the caller's startup context.
func MapContext4[R, A, B, C, D any](a Ref[A], b Ref[B], c Ref[C], d Ref[D], create func(context.Context, A, B, C, D) (R, error), ownership ...Ownership[R]) Ref[R] {
	if create == nil {
		return invalidDefinition[R]("MapContext4 requires a non-nil constructor")
	}
	return newDefinition([]*definition{a.node, b.node, c.node, d.node}, func(ctx context.Context, args []any) (R, error) {
		return create(ctx, inputValue[A](args, 0), inputValue[B](args, 1), inputValue[C](args, 2), inputValue[D](args, 3))
	}, ownership...)
}
