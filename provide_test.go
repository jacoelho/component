package component_test

import (
	"context"
	"sync/atomic"
	"testing"

	component "github.com/jacoelho/component"
)

type resultRefBuilder func(...component.Ownership[*testResult]) component.Ref[*testResult]

type resultFormCase struct {
	name  string
	build resultRefBuilder
	want  int
}

// testResult is an ordinary value with a lifecycle method set. The graph only
// owns it when a builder receives an explicit Managed option.
type testResult struct {
	value  int
	starts atomic.Int32
	stops  atomic.Int32
}

func (value *testResult) Start(context.Context) error {
	value.starts.Add(1)
	return nil
}

func (value *testResult) Stop(context.Context) error {
	value.stops.Add(1)
	return nil
}

func provideZero() *testResult { return &testResult{value: 2357} }

func tryProvideZero() (*testResult, error) { return provideZero(), nil }

func provideContextZero(context.Context) (*testResult, error) { return provideZero(), nil }

func provideOne(first int) *testResult { return &testResult{value: 1000 + first} }

func tryProvideOne(first int) (*testResult, error) { return provideOne(first), nil }

func provideContextOne(_ context.Context, first int) (*testResult, error) {
	return provideOne(first), nil
}

func provideTwo(first, second int) *testResult {
	return &testResult{value: first*100 + second}
}

func tryProvideTwo(first, second int) (*testResult, error) {
	return provideTwo(first, second), nil
}

func provideContextTwo(_ context.Context, first, second int) (*testResult, error) {
	return provideTwo(first, second), nil
}

func provideThree(first, second, third int) *testResult {
	return &testResult{value: first*100 + second*10 + third}
}

func tryProvideThree(first, second, third int) (*testResult, error) {
	return provideThree(first, second, third), nil
}

func provideContextThree(_ context.Context, first, second, third int) (*testResult, error) {
	return provideThree(first, second, third), nil
}

func provideFour(first, second, third, fourth int) *testResult {
	return &testResult{value: first*1000 + second*100 + third*10 + fourth}
}

func tryProvideFour(first, second, third, fourth int) (*testResult, error) {
	return provideFour(first, second, third, fourth), nil
}

func provideContextFour(_ context.Context, first, second, third, fourth int) (*testResult, error) {
	return provideFour(first, second, third, fourth), nil
}

func provideZeroRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Provide(provideZero, ownership...)
}

func tryProvideZeroRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.TryProvide(tryProvideZero, ownership...)
}

func provideContextZeroRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.ProvideContext(provideContextZero, ownership...)
}

func provideOneRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).Map(provideOne, ownership...)
}

func tryProvideOneRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).TryMap(tryProvideOne, ownership...)
}

func provideContextOneRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).MapContext(provideContextOne, ownership...)
}

func provideTwoRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).Map(provideTwo, ownership...)
}

func tryProvideTwoRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).TryMap(tryProvideTwo, ownership...)
}

func provideContextTwoRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).MapContext(provideContextTwo, ownership...)
}

func provideThreeRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).With(component.Value(5)).Map(provideThree, ownership...)
}

func tryProvideThreeRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).With(component.Value(5)).TryMap(tryProvideThree, ownership...)
}

func provideContextThreeRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).With(component.Value(5)).MapContext(provideContextThree, ownership...)
}

func provideFourRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).With(component.Value(5)).With(component.Value(7)).Map(provideFour, ownership...)
}

func tryProvideFourRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).With(component.Value(5)).With(component.Value(7)).TryMap(tryProvideFour, ownership...)
}

func provideContextFourRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Value(2).With(component.Value(3)).With(component.Value(5)).With(component.Value(7)).MapContext(provideContextFour, ownership...)
}

func resultFormCases() []resultFormCase {
	return []resultFormCase{
		{name: "provide/0", build: provideZeroRef, want: 2357},
		{name: "try-provide/0", build: tryProvideZeroRef, want: 2357},
		{name: "provide-context/0", build: provideContextZeroRef, want: 2357},
		{name: "provide/1", build: provideOneRef, want: 1002},
		{name: "try-provide/1", build: tryProvideOneRef, want: 1002},
		{name: "provide-context/1", build: provideContextOneRef, want: 1002},
		{name: "provide/2", build: provideTwoRef, want: 203},
		{name: "try-provide/2", build: tryProvideTwoRef, want: 203},
		{name: "provide-context/2", build: provideContextTwoRef, want: 203},
		{name: "provide/3", build: provideThreeRef, want: 235},
		{name: "try-provide/3", build: tryProvideThreeRef, want: 235},
		{name: "provide-context/3", build: provideContextThreeRef, want: 235},
		{name: "provide/4", build: provideFourRef, want: 2357},
		{name: "try-provide/4", build: tryProvideFourRef, want: 2357},
		{name: "provide-context/4", build: provideContextFourRef, want: 2357},
	}
}

func TestConstructionFormsAcrossArities(t *testing.T) {
	for _, form := range resultFormCases() {
		form := form
		t.Run(form.name, func(t *testing.T) {
			for _, managed := range []bool{false, true} {
				managed := managed
				t.Run(map[bool]string{false: "unmanaged", true: "managed"}[managed], func(t *testing.T) {
					var ref component.Ref[*testResult]
					if managed {
						ownership := component.Managed[*testResult]()
						ownership.Name = form.name
						ref = form.build(ownership)
					} else {
						ref = form.build()
					}

					rt, err := component.New(component.RuntimeOptions{}, ref)
					if err != nil {
						t.Fatalf("New returned an error: %v", err)
					}
					if err := rt.Start(context.Background()); err != nil {
						t.Fatalf("Start returned an error: %v", err)
					}
					got, err := rt.Value(ref)
					if err != nil {
						t.Fatalf("Value returned an error: %v", err)
					}
					if got == nil || got.value != form.want {
						t.Fatalf("Value = %#v, want value %d", got, form.want)
					}
					if err := rt.Stop(context.Background()); err != nil {
						t.Fatalf("Stop returned an error: %v", err)
					}

					wantCalls := int32(0)
					if managed {
						wantCalls = 1
					}
					if got := got.starts.Load(); got != wantCalls {
						t.Fatalf("Start calls = %d, want %d", got, wantCalls)
					}
					if got := got.stops.Load(); got != wantCalls {
						t.Fatalf("Stop calls = %d, want %d", got, wantCalls)
					}
				})
			}
		})
	}
}

type definedResultFactory func() *testResult

func TestDefinedFactoryAndManagedOwner(t *testing.T) {
	var factory definedResultFactory = provideZero
	definedRef := component.Provide(factory)
	managedRef := component.Provide(provideZero, component.Managed[*testResult]())
	rt, err := component.New(component.RuntimeOptions{}, definedRef, managedRef)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}
	defined, err := rt.Value(definedRef)
	if err != nil {
		t.Fatalf("Value(defined) returned an error: %v", err)
	}
	managed, err := rt.Value(managedRef)
	if err != nil {
		t.Fatalf("Value(managed) returned an error: %v", err)
	}
	if defined.starts.Load() != 0 || defined.stops.Load() != 0 {
		t.Fatalf("ordinary lifecycle value hooks ran: starts=%d stops=%d", defined.starts.Load(), defined.stops.Load())
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error: %v", err)
	}
	if managed.starts.Load() != 1 || managed.stops.Load() != 1 {
		t.Fatalf("managed hooks ran starts=%d stops=%d, want one each", managed.starts.Load(), managed.stops.Load())
	}
}

func TestContextNoErrorAdaptersAndMoreThanFourInputs(t *testing.T) {
	contextNoError := func(context.Context) *testResult { return &testResult{value: 2} }
	base := component.ProvideContext(func(ctx context.Context) (*testResult, error) {
		return contextNoError(ctx), nil
	})
	adapted := base.MapContext(func(_ context.Context, value *testResult) (*testResult, error) {
		return &testResult{value: value.value * 10}, nil
	})
	group := component.Value(2).With(component.Value(3)).With(component.Value(5)).Map(
		func(first, second, third int) int { return first*100 + second*10 + third },
	)
	large := group.With(component.Value(7)).With(component.Value(11)).Map(
		func(grouped, fourth, fifth int) *testResult {
			return &testResult{value: grouped*1000 + fourth*100 + fifth}
		},
		component.Managed[*testResult](),
	)
	rt, err := component.New(component.RuntimeOptions{}, adapted, large)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	if err := rt.Start(context.Background()); err != nil {
		t.Fatalf("Start returned an error: %v", err)
	}
	got, err := rt.Value(adapted)
	if err != nil || got == nil || got.value != 20 {
		t.Fatalf("adapted context value = (%#v, %v), want value 20", got, err)
	}
	if got.starts.Load() != 0 || got.stops.Load() != 0 {
		t.Fatalf("unmanaged context adapter hooks ran: starts=%d stops=%d", got.starts.Load(), got.stops.Load())
	}
	got, err = rt.Value(large)
	if err != nil || got == nil || got.value != 235711 {
		t.Fatalf("grouped value = (%#v, %v), want value 235711", got, err)
	}
	if err := rt.Stop(context.Background()); err != nil {
		t.Fatalf("Stop returned an error: %v", err)
	}
	if got.starts.Load() != 1 || got.stops.Load() != 1 {
		t.Fatalf("grouped managed hooks ran starts=%d stops=%d, want one each", got.starts.Load(), got.stops.Load())
	}
}
