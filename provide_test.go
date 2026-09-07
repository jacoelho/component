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

func provideValueZero() *testResult { return &testResult{value: 2357} }

func provideZero() (*testResult, error) { return provideValueZero(), nil }

func provideContextZero(context.Context) (*testResult, error) { return provideValueZero(), nil }

func provideValueOne(first int) *testResult { return &testResult{value: 1000 + first} }

func provideOne(first int) (*testResult, error) { return provideValueOne(first), nil }

func provideContextOne(_ context.Context, first int) (*testResult, error) {
	return provideValueOne(first), nil
}

func provideValueTwo(first, second int) *testResult {
	return &testResult{value: first*100 + second}
}

func provideTwo(first, second int) (*testResult, error) {
	return provideValueTwo(first, second), nil
}

func provideContextTwo(_ context.Context, first, second int) (*testResult, error) {
	return provideValueTwo(first, second), nil
}

func provideValueThree(first, second, third int) *testResult {
	return &testResult{value: first*100 + second*10 + third}
}

func provideThree(first, second, third int) (*testResult, error) {
	return provideValueThree(first, second, third), nil
}

func provideContextThree(_ context.Context, first, second, third int) (*testResult, error) {
	return provideValueThree(first, second, third), nil
}

func provideValueFour(first, second, third, fourth int) *testResult {
	return &testResult{value: first*1000 + second*100 + third*10 + fourth}
}

func provideFour(first, second, third, fourth int) (*testResult, error) {
	return provideValueFour(first, second, third, fourth), nil
}

func provideContextFour(_ context.Context, first, second, third, fourth int) (*testResult, error) {
	return provideValueFour(first, second, third, fourth), nil
}

func provideValueZeroRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.ProvideValue(provideValueZero, ownership...)
}

func provideZeroRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Provide(provideZero, ownership...)
}

func provideContextZeroRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.ProvideContext(provideContextZero, ownership...)
}

func provideValueOneRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.MapValue(component.Value(2), provideValueOne, ownership...)
}

func provideOneRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Map(component.Value(2), provideOne, ownership...)
}

func provideContextOneRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.MapContext(component.Value(2), provideContextOne, ownership...)
}

func provideValueTwoRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.MapValue2(component.Value(2), component.Value(3), provideValueTwo, ownership...)
}

func provideTwoRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Map2(component.Value(2), component.Value(3), provideTwo, ownership...)
}

func provideContextTwoRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.MapContext2(component.Value(2), component.Value(3), provideContextTwo, ownership...)
}

func provideValueThreeRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.MapValue3(component.Value(2), component.Value(3), component.Value(5), provideValueThree, ownership...)
}

func provideThreeRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Map3(component.Value(2), component.Value(3), component.Value(5), provideThree, ownership...)
}

func provideContextThreeRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.MapContext3(component.Value(2), component.Value(3), component.Value(5), provideContextThree, ownership...)
}

func provideValueFourRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.MapValue4(component.Value(2), component.Value(3), component.Value(5), component.Value(7), provideValueFour, ownership...)
}

func provideFourRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.Map4(component.Value(2), component.Value(3), component.Value(5), component.Value(7), provideFour, ownership...)
}

func provideContextFourRef(ownership ...component.Ownership[*testResult]) component.Ref[*testResult] {
	return component.MapContext4(component.Value(2), component.Value(3), component.Value(5), component.Value(7), provideContextFour, ownership...)
}

func resultFormCases() []resultFormCase {
	return []resultFormCase{
		{name: "ProvideValue", build: provideValueZeroRef, want: 2357},
		{name: "Provide", build: provideZeroRef, want: 2357},
		{name: "ProvideContext", build: provideContextZeroRef, want: 2357},
		{name: "MapValue", build: provideValueOneRef, want: 1002},
		{name: "Map", build: provideOneRef, want: 1002},
		{name: "MapContext", build: provideContextOneRef, want: 1002},
		{name: "MapValue2", build: provideValueTwoRef, want: 203},
		{name: "Map2", build: provideTwoRef, want: 203},
		{name: "MapContext2", build: provideContextTwoRef, want: 203},
		{name: "MapValue3", build: provideValueThreeRef, want: 235},
		{name: "Map3", build: provideThreeRef, want: 235},
		{name: "MapContext3", build: provideContextThreeRef, want: 235},
		{name: "MapValue4", build: provideValueFourRef, want: 2357},
		{name: "Map4", build: provideFourRef, want: 2357},
		{name: "MapContext4", build: provideContextFourRef, want: 2357},
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

					rt, err := component.New(ref)
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
	var factory definedResultFactory = provideValueZero
	definedRef := component.ProvideValue(factory)
	managedRef := component.ProvideValue(provideValueZero, component.Managed[*testResult]())
	rt, err := component.New(definedRef, managedRef)
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
	adapted := component.MapContext(base, func(_ context.Context, value *testResult) (*testResult, error) {
		return &testResult{value: value.value * 10}, nil
	})
	group := component.MapValue3(component.Value(2), component.Value(3), component.Value(5),
		func(first, second, third int) int { return first*100 + second*10 + third },
	)
	large := component.MapValue3(group, component.Value(7), component.Value(11),
		func(grouped, fourth, fifth int) *testResult {
			return &testResult{value: grouped*1000 + fourth*100 + fifth}
		},
		component.Managed[*testResult](),
	)
	rt, err := component.New(adapted, large)
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
