package component_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	component "github.com/jacoelho/component"
)

type constructionResource struct {
	id      int
	startFn func(context.Context) error
	stopFn  func(context.Context) error
}

type constructionInterface interface {
	ID() int
}

func (r *constructionResource) ID() int {
	if r == nil {
		return 0
	}
	return r.id
}

func (r *constructionResource) Start(ctx context.Context) error {
	if r.startFn == nil {
		return nil
	}
	return r.startFn(ctx)
}

func (r *constructionResource) Stop(ctx context.Context) error {
	if r.stopFn == nil {
		return nil
	}
	return r.stopFn(ctx)
}

func newTestRuntime(t *testing.T, roots ...component.Root) *component.Runtime {
	t.Helper()
	rt, err := component.New(roots...)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	return rt
}

func requireSentinel(t *testing.T, err, want error) {
	t.Helper()
	if err == nil || !errors.Is(err, want) {
		t.Fatalf("error did not contain sentinel %v; got %v", want, err)
	}
}

func TestDefinitionsDoNotRunFactoriesUntilStart(t *testing.T) {
	var calls atomic.Int32
	ref := component.ProvideValue(func() int {
		calls.Add(1)
		return 42
	})

	rt := newTestRuntime(t, ref)
	if got := calls.Load(); got != 0 {
		t.Fatalf("factory calls after New = %d, want 0", got)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("factory calls after Start = %d, want 1", got)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestNewValidatesAllRootsBeforeRunningAnyFactory(t *testing.T) {
	var calls atomic.Int32
	valid := component.ProvideValue(func() int {
		calls.Add(1)
		return 7
	})
	var invalid component.Ref[string]

	_, err := component.New(valid, invalid)
	requireSentinel(t, err, component.ErrInvalidReference)
	if got := calls.Load(); got != 0 {
		t.Fatalf("factory calls after invalid New = %d, want 0", got)
	}
}

func TestValueIsBorrowedAndFunctionValuesAreNotInvoked(t *testing.T) {
	var calls atomic.Int32
	value := func() int {
		calls.Add(1)
		return 99
	}
	ref := component.Value(value)
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	got, err := rt.Value(ref)
	if err != nil {
		t.Fatalf("Value returned an error")
	}
	if reflect.ValueOf(got).Pointer() != reflect.ValueOf(value).Pointer() {
		t.Fatalf("Value returned a different function value")
	}
	if got := calls.Load(); got != 0 {
		t.Fatalf("function value was invoked %d times", got)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestSharedRefsAndRepeatedRootsMaterializeOnce(t *testing.T) {
	var created, stopped atomic.Int32
	base := component.ProvideValue(func() *constructionResource {
		created.Add(1)
		return &constructionResource{id: 17, stopFn: func(context.Context) error {
			stopped.Add(1)
			return nil
		}}
	}, component.Managed[*constructionResource]())
	left := component.MapValue(base, func(r *constructionResource) int { return r.id + 1 })
	right := component.MapValue(base, func(r *constructionResource) int { return r.id + 2 })

	rt := newTestRuntime(t, left, right, left)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	if got := created.Load(); got != 1 {
		t.Fatalf("shared factory calls = %d, want 1", got)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
	if got := stopped.Load(); got != 1 {
		t.Fatalf("shared stop calls = %d, want 1", got)
	}
}

func TestUnusedDefinitionsAreNotReachable(t *testing.T) {
	var used, unused atomic.Int32
	usedRef := component.ProvideValue(func() int {
		used.Add(1)
		return 1
	})
	_ = component.ProvideValue(func() int {
		unused.Add(1)
		return 2
	})

	rt := newTestRuntime(t, usedRef)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	if used.Load() != 1 || unused.Load() != 0 {
		t.Fatalf("reachable calls = %d, unreachable calls = %d; want 1 and 0", used.Load(), unused.Load())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestPureMapPreservesTheOwnedDependencyLifetime(t *testing.T) {
	var stopped atomic.Int32
	owner := component.ProvideValue(func() *constructionResource {
		return &constructionResource{id: 23, stopFn: func(context.Context) error {
			stopped.Add(1)
			return nil
		}}
	}, component.Managed[*constructionResource]())
	view := component.MapValue(owner, func(r *constructionResource) constructionInterface { return r })

	rt := newTestRuntime(t, view)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	got, err := rt.Value(view)
	if err != nil || got == nil || got.ID() != 23 {
		t.Fatalf("mapped value was not available")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
	if got := stopped.Load(); got != 1 {
		t.Fatalf("owner stop calls = %d, want 1", got)
	}
}

func TestIndependentRuntimesConstructIndependentOwnedValues(t *testing.T) {
	var next atomic.Int32
	var stopped atomic.Int32
	ref := component.ProvideValue(func() *constructionResource {
		return &constructionResource{id: int(next.Add(1)), stopFn: func(context.Context) error {
			stopped.Add(1)
			return nil
		}}
	}, component.Managed[*constructionResource]())
	one := newTestRuntime(t, ref)
	two := newTestRuntime(t, ref)
	if err := one.Start(context.Background()); err != nil {
		t.Fatalf("first Start returned an error")
	}
	if err := two.Start(context.Background()); err != nil {
		t.Fatalf("second Start returned an error")
	}
	oneValue, err := one.Value(ref)
	if err != nil {
		t.Fatalf("first Value returned an error")
	}
	twoValue, err := two.Value(ref)
	if err != nil {
		t.Fatalf("second Value returned an error")
	}
	if oneValue == twoValue || oneValue.id == twoValue.id {
		t.Fatalf("independent runtimes shared an owned value")
	}
	if err := one.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop returned an error")
	}
	if err := two.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop returned an error")
	}
	if got := stopped.Load(); got != 2 {
		t.Fatalf("stop calls = %d, want 2", got)
	}
}

func TestInputsPreserveArgumentOrderAndRepeatedArguments(t *testing.T) {
	var calls atomic.Int32
	input := component.Value(13)
	ref := component.MapValue2(input, input, func(first, second int) int {
		calls.Add(1)
		return first*100 + second
	})
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	got, err := rt.Value(ref)
	if err != nil {
		t.Fatalf("Value returned an error")
	}
	if got != 1313 {
		t.Fatalf("mapped arguments produced %d, want 1313", got)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("map calls = %d, want 1", got)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestTypedInterfaceValuesAndNilInterfacesRemainValid(t *testing.T) {
	concrete := component.Value(&constructionResource{id: 31})
	view := component.MapValue(concrete, func(r *constructionResource) constructionInterface { return r })
	var nilInterface constructionInterface
	nilRef := component.Value(nilInterface)

	rt := newTestRuntime(t, view, nilRef)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	got, err := rt.Value(view)
	if err != nil || got == nil || got.ID() != 31 {
		t.Fatalf("interface adaptation did not preserve the concrete value")
	}
	nilGot, err := rt.Value(nilRef)
	if err != nil {
		t.Fatalf("nil interface Value returned an error")
	}
	if nilGot != nil {
		t.Fatalf("nil interface Value was changed")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestTypedNilUnmanagedValuesAreOrdinaryValues(t *testing.T) {
	var value *constructionResource
	ref := component.Value(value)
	rt := newTestRuntime(t, ref)
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	got, err := rt.Value(ref)
	if err != nil {
		t.Fatalf("Value returned an error")
	}
	if got != nil {
		t.Fatalf("typed nil was changed")
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
}

func TestOwnershipValidationHappensAtNew(t *testing.T) {
	var zero component.Ownership[*constructionResource]
	withoutOwnership := component.ProvideValue(func() *constructionResource {
		return &constructionResource{}
	}, zero)
	_, err := component.New(withoutOwnership)
	requireSentinel(t, err, component.ErrInvalidDefinition)

	withTwo := component.ProvideValue(func() *constructionResource {
		return &constructionResource{}
	},
		component.Managed[*constructionResource](),
		component.Managed[*constructionResource](),
	)
	_, err = component.New(withTwo)
	requireSentinel(t, err, component.ErrInvalidDefinition)
}

func TestNilConstructorsAreRejectedBeforeStart(t *testing.T) {
	input := component.Value(7)
	cases := []struct {
		name string
		ref  component.Root
	}{
		{name: "ProvideValue", ref: component.ProvideValue[int](nil)},
		{name: "Provide", ref: component.Provide[int](nil)},
		{name: "ProvideContext", ref: component.ProvideContext[int](nil)},
		{name: "MapValue", ref: component.MapValue[int](input, nil)},
		{name: "Map", ref: component.Map[int](input, nil)},
		{name: "MapContext", ref: component.MapContext[int](input, nil)},
		{name: "MapValue2", ref: component.MapValue2[int](input, input, nil)},
		{name: "Map2", ref: component.Map2[int](input, input, nil)},
		{name: "MapContext2", ref: component.MapContext2[int](input, input, nil)},
		{name: "MapValue3", ref: component.MapValue3[int](input, input, input, nil)},
		{name: "Map3", ref: component.Map3[int](input, input, input, nil)},
		{name: "MapContext3", ref: component.MapContext3[int](input, input, input, nil)},
		{name: "MapValue4", ref: component.MapValue4[int](input, input, input, input, nil)},
		{name: "Map4", ref: component.Map4[int](input, input, input, input, nil)},
		{name: "MapContext4", ref: component.MapContext4[int](input, input, input, input, nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := component.New(tc.ref)
			requireSentinel(t, err, component.ErrInvalidDefinition)
		})
	}
}

func TestManagedNilResultIsRejectedWithoutHooks(t *testing.T) {
	ref := component.ProvideValue(func() *constructionResource {
		return nil
	}, component.Managed[*constructionResource]())
	rt := newTestRuntime(t, ref)
	err := rt.Start(context.Background())
	requireSentinel(t, err, component.ErrInvalidValue)
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop after rejected nil result returned an error")
	}
}

func TestRuntimeValueStateAndForeignReferences(t *testing.T) {
	ref := component.Value(41)
	foreign := component.Value(41)
	rt := newTestRuntime(t, ref)

	if _, err := rt.Value(ref); err == nil {
		t.Fatalf("Value before Start unexpectedly succeeded")
	} else {
		requireSentinel(t, err, component.ErrUnavailable)
	}
	if _, err := rt.Value(foreign); err == nil {
		t.Fatalf("foreign Value before Start unexpectedly succeeded")
	} else {
		requireSentinel(t, err, component.ErrUnavailable)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error")
	}
	if got, err := rt.Value(ref); err != nil || got != 41 {
		t.Fatalf("running Value = %d, error present=%t", got, err != nil)
	}
	if _, err := rt.Value(foreign); err == nil {
		t.Fatalf("Value for a foreign ref unexpectedly succeeded")
	} else {
		requireSentinel(t, err, component.ErrInvalidReference)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error")
	}
	if _, err := rt.Value(ref); err == nil {
		t.Fatalf("Value after Stop unexpectedly succeeded")
	} else {
		requireSentinel(t, err, component.ErrUnavailable)
	}
}

func TestRuntimeCopySharesStateAndIdentity(t *testing.T) {
	var starts, stops atomic.Int32
	ref := component.ProvideValue(func() *constructionResource {
		return &constructionResource{id: 5, startFn: func(context.Context) error {
			starts.Add(1)
			return nil
		}, stopFn: func(context.Context) error {
			stops.Add(1)
			return nil
		}}
	}, component.Managed[*constructionResource]())
	rt := newTestRuntime(t, ref)
	copy := *rt
	if err := copy.Start(context.Background()); err != nil {
		t.Fatalf("Start on copy returned an error")
	}
	if _, err := rt.Value(ref); err != nil {
		t.Fatalf("Value through original copy returned an error")
	}
	if err := rt.Start(context.Background()); err == nil {
		t.Fatalf("Start through original copy unexpectedly succeeded")
	} else {
		requireSentinel(t, err, component.ErrAlreadyStarted)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop through original copy returned an error")
	}
	if starts.Load() != 1 || stops.Load() != 1 {
		t.Fatalf("copy changed lifecycle counts: starts=%d stops=%d", starts.Load(), stops.Load())
	}
}

func TestNilAndZeroRuntimeAreInvalid(t *testing.T) {
	var rt *component.Runtime
	if err := rt.Start(context.Background()); err == nil {
		t.Fatalf("nil Runtime Start unexpectedly succeeded")
	} else {
		requireSentinel(t, err, component.ErrInvalidRuntime)
	}
	var zero component.Runtime
	if err := zero.Start(context.Background()); err == nil {
		t.Fatalf("zero Runtime Start unexpectedly succeeded")
	} else {
		requireSentinel(t, err, component.ErrInvalidRuntime)
	}
}
